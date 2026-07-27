// Package syncer orchestrates per-feed fetch + write across feeds.
package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/mmcdole/gofeed"

	"github.com/isdg/hr/internal/article"
	"github.com/isdg/hr/internal/cache"
	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/errlog"
	"github.com/isdg/hr/internal/feed"
	"github.com/isdg/hr/internal/meta"
	"github.com/isdg/hr/internal/tombstone"
	"github.com/isdg/hr/internal/vault"
)

// defaultConcurrency is the worker count used when Options.Concurrency
// is left at zero.
const defaultConcurrency = 8

type Options struct {
	Vault     *vault.Vault
	Config    *config.Config
	FeedName  string // empty = all feeds
	UserAgent string
	Force     bool // ignore cache; refetch even if not modified

	// Concurrency bounds how many feeds are fetched in parallel. Zero or
	// negative means defaultConcurrency; it is also clamped to the feed
	// count.
	Concurrency int

	// OnFeedDone, if set, is called once per feed as it finishes so
	// callers can show live progress. i is the 1-based completion order
	// (the Nth feed to finish, not its config index); total is the feed
	// count. Feeds run concurrently, but OnFeedDone calls are serialized,
	// so the callback needs no locking of its own.
	OnFeedDone func(i, total int, fr FeedResult)
}

type FeedResult struct {
	Name string
	URL  string
	// New and Existing count only the items this fetch's feed response
	// contained: written now, and already on disk. A feed serving the
	// latest N entries therefore has New+Existing == N regardless of how
	// much of it is archived, so neither is the feed's article count.
	New      int
	Existing int

	// Total is the feed's article count on disk after the sync — the whole
	// archive, including articles older than anything the feed still
	// serves. Zero when the feed errored or was not modified, since
	// nothing was counted.
	Total       int
	NotModified bool
	Err         error
}

type Result struct {
	Feeds []FeedResult
}

func Run(ctx context.Context, opts Options) (*Result, error) {
	elog := errlog.New(filepath.Join(opts.Vault.MetaDir(), "err.txt"))

	c, err := cache.Load(opts.Vault.CachePath())
	if err != nil {
		elog.Write("cache.load", err)
		return nil, fmt.Errorf("load cache: %w", err)
	}

	feeds, err := selectFeeds(opts.Config.Feeds, opts.FeedName)
	if err != nil {
		return nil, err
	}

	// One snapshot of feeds/ for the whole run: workers create
	// directories as they go, so re-walking mid-sync would give
	// different answers to the same question.
	loc, err := opts.Vault.Locate()
	if err != nil {
		elog.Write("vault.locate", err)
		return nil, fmt.Errorf("locate feed directories: %w", err)
	}

	total := len(feeds)
	result := &Result{Feeds: make([]FeedResult, total)}

	workers := opts.Concurrency
	if workers <= 0 {
		workers = defaultConcurrency
	}
	if workers > total {
		workers = total
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex // serializes OnFeedDone and the done counter
		done int
	)
	jobs := make(chan int)

	worker := func() {
		defer wg.Done()
		// Each worker owns its converter; md.Converter is not documented
		// as goroutine-safe.
		conv := md.NewConverter("", true, nil)
		for i := range jobs {
			fr := syncFeed(ctx, opts, feeds[i], c, conv, elog, loc)
			result.Feeds[i] = fr // distinct index per feed: race-free
			if opts.OnFeedDone != nil {
				mu.Lock()
				done++
				opts.OnFeedDone(done, total, fr)
				mu.Unlock()
			}
		}
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go worker()
	}
	for i := range feeds {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	if err := c.Save(); err != nil {
		elog.Write("cache.save", err)
		return result, fmt.Errorf("save cache: %w", err)
	}
	return result, nil
}

func selectFeeds(
	all []config.Feed, name string,
) ([]config.Feed, error) {
	if name == "" {
		return all, nil
	}
	for _, f := range all {
		if f.Name == name {
			return []config.Feed{f}, nil
		}
	}
	return nil, fmt.Errorf("feed %q not found in config", name)
}

func syncFeed(
	ctx context.Context,
	opts Options,
	f config.Feed,
	c *cache.Cache,
	conv *md.Converter,
	elog *errlog.Log,
	loc *vault.FeedLocator,
) FeedResult {
	fr := FeedResult{Name: f.Name, URL: f.URL}
	tag := "feed:" + f.Name

	entry := c.Get(f.Name)
	fopts := feed.Options{UserAgent: opts.UserAgent}
	if !opts.Force {
		fopts.ETag = entry.ETag
		fopts.LastModified = entry.LastModified
	}
	res, err := feed.Fetch(ctx, f.URL, fopts)
	if err != nil {
		elog.Write(tag+":fetch", err)
		fr.Err = err
		return fr
	}
	if res.NotModified {
		fr.NotModified = true
		return fr
	}

	feedDir, err := loc.Dir(f.Name, f.Group)
	if err != nil {
		elog.Write(tag+":locate", err)
		fr.Err = err
		return fr
	}
	deleted, _ := tombstone.DeletedIDs(feedDir)

	for _, item := range res.Feed.Items {
		a := itemToArticle(item, f.Name, conv)
		if deleted[a.ID()] {
			continue // tombstoned: sync-safe delete
		}
		written, path, err := article.Write(feedDir, a)
		if err != nil {
			elog.Write(tag+":write", err)
			fr.Err = err
			return fr
		}
		if written {
			fr.New++
		} else {
			fr.Existing++
		}
		if _, err := meta.WriteIfAbsent(path); err != nil {
			elog.Write(tag+":meta", err)
			fr.Err = err
			return fr
		}
		if err := writeRawHTML(opts.Vault, item, a); err != nil {
			elog.Write(tag+":raw", err)
		}
	}

	// Count the archive on disk rather than deriving it from New+Existing,
	// which only ever covers the items this response carried.
	fr.Total = countArticles(feedDir)

	c.Set(f.Name, cache.Entry{
		ETag:         res.ETag,
		LastModified: res.LastModified,
		FetchedAt:    time.Now().UTC(),
	})
	return fr
}

// countArticles returns how many articles the feed directory holds. A
// glob failure yields 0 rather than an error: the count is display-only
// and must never fail a sync that otherwise succeeded.
func countArticles(feedDir string) int {
	hits, err := filepath.Glob(filepath.Join(feedDir, "*.md"))
	if err != nil {
		return 0
	}
	return len(hits)
}

// writeRawHTML stashes the original feed body HTML at
// <vault>/.hr/raw/<id>.html the first time we see an item. Idempotent
// (skips if the file already exists). Cheap insurance against future
// HTML→markdown conversion bugs.
func writeRawHTML(
	v *vault.Vault, item *gofeed.Item, a *article.Article,
) error {
	html := item.Content
	if html == "" {
		html = item.Description
	}
	if html == "" {
		return nil
	}
	rawPath := v.RawPath(a.FeedName, a.Filename())
	// Dedup by the stable id (like article.Write), so an edited+renamed
	// article isn't re-stashed under its original name on the next sync.
	if hits, _ := filepath.Glob(
		filepath.Join(filepath.Dir(rawPath), "*-"+a.ID()+".html")); len(hits) > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(rawPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rawPath, []byte(html), 0o644)
}

func itemToArticle(
	item *gofeed.Item, feedName string, conv *md.Converter,
) *article.Article {
	title := item.Title
	if title == "" {
		title = "(untitled)"
	}
	return &article.Article{
		Title:     title,
		URL:       item.Link,
		Published: derivePublished(item),
		FeedName:  feedName,
		GUID:      item.GUID,
		Body:      extractBody(item, conv),
	}
}

func extractBody(item *gofeed.Item, conv *md.Converter) string {
	html := item.Content
	if html == "" {
		html = item.Description
	}
	if html == "" {
		return ""
	}
	out, err := conv.ConvertString(html)
	if err != nil {
		return html
	}
	return out
}

func derivePublished(item *gofeed.Item) time.Time {
	if item.PublishedParsed != nil {
		return *item.PublishedParsed
	}
	if item.UpdatedParsed != nil {
		return *item.UpdatedParsed
	}
	return time.Now().UTC()
}
