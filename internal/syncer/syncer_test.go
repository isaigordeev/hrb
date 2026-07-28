package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/isdg/hr/internal/cache"
	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/vault"
)

// testServer serves three routes: /ok returns a 200 feed with two items
// and an ETag, /notmod always replies 304, and /err always fails with
// 500.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		fmtFeed(w, "ok")
	})
	mux.HandleFunc("/ok2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v2"`)
		fmtFeed(w, "ok2")
	})
	mux.HandleFunc("/notmod", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("/err", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	return httptest.NewServer(mux)
}

func fmtFeed(w http.ResponseWriter, name string) {
	body := "<?xml version=\"1.0\"?>\n<rss version=\"2.0\"><channel>\n" +
		"<title>" + name + "</title>\n" +
		"<item><title>First " + name + "</title>" +
		"<link>http://example.com/" + name + "/1</link>" +
		"<guid>" + name + "-1</guid></item>\n" +
		"<item><title>Second " + name + "</title>" +
		"<link>http://example.com/" + name + "/2</link>" +
		"<guid>" + name + "-2</guid></item>\n" +
		"</channel></rss>"
	_, _ = w.Write([]byte(body))
}

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	v := &vault.Vault{Root: t.TempDir()}
	for _, d := range []string{v.FeedsDir(), v.MetaDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return v
}

// TestRunConcurrent verifies that concurrent sync produces correct,
// deterministically-ordered results across a mix of feed outcomes and
// updates the cache without data races (run with -race). It runs at
// several concurrency levels to cover both the sequential and pooled
// paths.
func TestRunConcurrent(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	feeds := []config.Feed{
		{Name: "alpha", URL: srv.URL + "/ok"},
		{Name: "bravo", URL: srv.URL + "/notmod"},
		{Name: "charlie", URL: srv.URL + "/err"},
		{Name: "delta", URL: srv.URL + "/ok2"},
	}

	for _, jobs := range []int{1, 2, 8} {
		t.Run("jobs="+strconv.Itoa(jobs), func(t *testing.T) {
			v := newVault(t)
			res, err := Run(context.Background(), Options{
				Vault:       v,
				Config:      &config.Config{Feeds: feeds},
				UserAgent:   "hr-test",
				Concurrency: jobs,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			// Result order matches config order regardless of completion.
			if len(res.Feeds) != len(feeds) {
				t.Fatalf("got %d feeds, want %d", len(res.Feeds), len(feeds))
			}
			for i, want := range feeds {
				if res.Feeds[i].Name != want.Name {
					t.Errorf("feed[%d] = %q, want %q (order not preserved)",
						i, res.Feeds[i].Name, want.Name)
				}
			}

			alpha, bravo, charlie, delta := res.Feeds[0], res.Feeds[1],
				res.Feeds[2], res.Feeds[3]
			if alpha.New != 2 || alpha.Existing != 0 || alpha.Err != nil {
				t.Errorf("alpha = %+v, want 2 new / 0 existing / no err", alpha)
			}
			if !bravo.NotModified || bravo.Err != nil {
				t.Errorf("bravo = %+v, want NotModified", bravo)
			}
			if charlie.Err == nil {
				t.Errorf("charlie = %+v, want an error", charlie)
			}
			if delta.New != 2 || delta.Err != nil {
				t.Errorf("delta = %+v, want 2 new / no err", delta)
			}

			// Cache holds entries for the two modified feeds.
			c, err := cache.Load(v.CachePath())
			if err != nil {
				t.Fatalf("load cache: %v", err)
			}
			if got := c.Get("alpha").ETag; got != `"v1"` {
				t.Errorf("alpha ETag = %q, want \"v1\"", got)
			}
			if c.Get("alpha").FetchedAt.IsZero() {
				t.Errorf("alpha FetchedAt not set")
			}

			// Articles landed on disk under their feed dir.
			hits, _ := filepath.Glob(
				filepath.Join(v.FeedsDir(), "alpha", "*.md"))
			if len(hits) != 2 {
				t.Errorf("alpha articles on disk = %d, want 2", len(hits))
			}
		})
	}
}

// TestRunNoFeeds guards the empty-feed-list edge (worker count clamps to
// zero without deadlocking).
func TestRunNoFeeds(t *testing.T) {
	v := newVault(t)
	res, err := Run(context.Background(), Options{
		Vault:  v,
		Config: &config.Config{Feeds: nil},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Feeds) != 0 {
		t.Fatalf("got %d feeds, want 0", len(res.Feeds))
	}
}

// A feed whose directory has been moved into a group must keep syncing
// into that directory — not resurrect a flat one at feeds/<name>. This
// is what makes `hr mv` (or a plain `mv`) a durable way to organize a
// vault.
func TestRunWritesIntoExistingGroupedDir(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	v := newVault(t)

	// Pre-place the feed under a group, as a move would leave it.
	grouped := filepath.Join(v.FeedsDir(), "humans", "alpha")
	if err := os.MkdirAll(grouped, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(grouped, "2019-01-01-seed-deadbeef.md")
	if err := os.WriteFile(seed, []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		Vault:  v,
		Config: &config.Config{Feeds: []config.Feed{{Name: "alpha", URL: srv.URL + "/ok"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Feeds[0].Err != nil || res.Feeds[0].New != 2 {
		t.Fatalf("alpha = %+v, want 2 new / no err", res.Feeds[0])
	}

	hits, _ := filepath.Glob(filepath.Join(grouped, "*.md"))
	if len(hits) != 3 { // seed + 2 fetched
		t.Errorf("grouped dir holds %d .md files, want 3", len(hits))
	}
	if _, err := os.Stat(filepath.Join(v.FeedsDir(), "alpha")); !os.IsNotExist(err) {
		t.Error("sync recreated a flat feeds/alpha alongside the grouped dir")
	}
}

// With no directory yet, a feed is created under its configured group.
func TestRunPlacesNewFeedInConfiguredGroup(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	v := newVault(t)

	_, err := Run(context.Background(), Options{
		Vault: v,
		Config: &config.Config{Feeds: []config.Feed{
			{Name: "alpha", URL: srv.URL + "/ok", Group: "sites"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	hits, _ := filepath.Glob(
		filepath.Join(v.FeedsDir(), "sites", "alpha", "*.md"))
	if len(hits) != 2 {
		t.Errorf("feeds/sites/alpha holds %d articles, want 2", len(hits))
	}
}

// FeedResult.Total is the feed's article count on disk, not the size of
// the feed response. A feed serving only its latest entries used to make
// `hr sync` report New+Existing as the "total", which undercounts every
// archived article the feed no longer lists.
func TestRunTotalCountsArchiveNotResponse(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	v := newVault(t)

	// Three articles already on disk that the feed will not return.
	dir := filepath.Join(v.FeedsDir(), "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"2001-01-01-old-one-aaaaaaa1.md",
		"2002-01-01-old-two-aaaaaaa2.md",
		"2003-01-01-old-three-aaaaaa3.md",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Run(context.Background(), Options{
		Vault:  v,
		Config: &config.Config{Feeds: []config.Feed{{Name: "alpha", URL: srv.URL + "/ok"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := res.Feeds[0]

	// The response carried 2 items, both new.
	if fr.New != 2 || fr.Existing != 0 {
		t.Errorf("New=%d Existing=%d, want 2/0", fr.New, fr.Existing)
	}
	// 3 pre-existing + 2 fetched.
	if fr.Total != 5 {
		t.Errorf("Total = %d, want 5 (the archive, not the response)", fr.Total)
	}
	if fr.Total == fr.New+fr.Existing {
		t.Error("Total is still New+Existing; the archive is being undercounted")
	}

	// And it matches what's actually on disk.
	hits, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	if fr.Total != len(hits) {
		t.Errorf("Total = %d but %d .md files on disk", fr.Total, len(hits))
	}
}

// Re-syncing an unchanged feed reports every item as Existing, and Total
// still reflects the archive.
func TestRunTotalOnResync(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	v := newVault(t)
	cfg := &config.Config{Feeds: []config.Feed{{Name: "alpha", URL: srv.URL + "/ok"}}}

	if _, err := Run(context.Background(), Options{Vault: v, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	// Force a refetch so the items come back and land as Existing.
	res, err := Run(context.Background(), Options{Vault: v, Config: cfg, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	fr := res.Feeds[0]
	if fr.New != 0 || fr.Existing != 2 {
		t.Errorf("New=%d Existing=%d, want 0/2", fr.New, fr.Existing)
	}
	if fr.Total != 2 {
		t.Errorf("Total = %d, want 2", fr.Total)
	}
}

// TestRunManualFeed verifies that a feed tagged "manual" is reported as
// such without any fetch, that its on-disk archive is still counted, and
// that it gets no cache entry.
func TestRunManualFeed(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	v := newVault(t)
	// Pre-place a hand-written article so Total has something to count.
	dir := filepath.Join(v.FeedsDir(), "humans", "curated")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	art := filepath.Join(dir, "2024-01-01-by-hand-deadbeef.md")
	if err := os.WriteFile(art, []byte("---\ntitle: By hand\n---\n"), 0o644); err != nil {
		t.Fatalf("write article: %v", err)
	}

	feeds := []config.Feed{
		{Name: "curated", Tags: []string{config.TagManual}, Group: "humans"},
		{Name: "alpha", URL: srv.URL + "/ok"},
	}
	res, err := Run(context.Background(), Options{
		Vault:     v,
		Config:    &config.Config{Feeds: feeds},
		UserAgent: "hr-test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	curated := res.Feeds[0]
	if !curated.Manual {
		t.Errorf("curated.Manual = false, want true")
	}
	if curated.Err != nil {
		t.Errorf("curated.Err = %v, want nil (nothing is fetched)", curated.Err)
	}
	if curated.New != 0 || curated.Existing != 0 || curated.NotModified {
		t.Errorf("curated = %+v, want no fetch activity", curated)
	}
	if curated.Total != 1 {
		t.Errorf("curated.Total = %d, want 1 (the hand-written article)",
			curated.Total)
	}

	// The URL-backed feed alongside it still syncs normally.
	if res.Feeds[1].New != 2 {
		t.Errorf("alpha.New = %d, want 2", res.Feeds[1].New)
	}

	// No cache entry: there was no response to remember.
	c, err := cache.Load(v.CachePath())
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if got := c.Get("curated"); got.ETag != "" || !got.FetchedAt.IsZero() {
		t.Errorf("curated cache entry = %+v, want empty", got)
	}
}

// TestFeedIsManual covers the tag lookup: case-insensitive, whitespace
// tolerant, and false for feeds carrying other tags.
func TestFeedIsManual(t *testing.T) {
	cases := []struct {
		tags []string
		want bool
	}{
		{nil, false},
		{[]string{"manual"}, true},
		{[]string{"Manual"}, true},
		{[]string{" manual "}, true},
		{[]string{"ai", "manual"}, true},
		{[]string{"manually-curated"}, false},
		{[]string{"ai"}, false},
	}
	for _, c := range cases {
		if got := (config.Feed{Tags: c.tags}).IsManual(); got != c.want {
			t.Errorf("Feed{Tags: %v}.IsManual() = %v, want %v",
				c.tags, got, c.want)
		}
	}
}
