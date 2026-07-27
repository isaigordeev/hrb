package vault

// Feed directories may be nested under feeds/ to group related feeds,
// e.g. feeds/humans/matklad or feeds/sites/lobsters. The directory on
// disk is the source of truth: hr locates a feed by its directory
// *name* at any depth, so regrouping is a plain `mv` (or `git mv`) and
// needs no config change. That also means feed directories hr never
// fetched — a hand-curated shelf of essays, say — group exactly like
// synced ones. hr.toml's optional `group` only decides where a feed
// that has no directory yet gets created on its first sync.
//
// Classification rule: a directory under feeds/ holding at least one
// regular file is a feed directory; one holding only subdirectories is
// a group.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// FeedLocator maps feed names to their directories from a single
// snapshot of feeds/. Sync resolves every feed against one locator so
// concurrent workers neither re-walk the tree nor observe directories
// their peers just created.
type FeedLocator struct {
	feedsDir string
	dirs     map[string][]string
	rel      func(string) string
}

// Locate walks feeds/ and snapshots where every feed directory lives.
// A missing feeds/ directory yields an empty locator, not an error.
func (v *Vault) Locate() (*FeedLocator, error) {
	l := &FeedLocator{
		feedsDir: v.FeedsDir(),
		dirs:     map[string][]string{},
		rel:      v.Rel,
	}
	if err := l.walk(l.feedsDir); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *FeedLocator) walk(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) && dir == l.feedsDir {
			return nil
		}
		return err
	}
	hasFile := false
	var subs []string
	for _, e := range entries {
		if e.IsDir() {
			subs = append(subs, filepath.Join(dir, e.Name()))
		} else {
			hasFile = true
		}
	}
	if dir != l.feedsDir && hasFile {
		// A feed directory: its contents are articles, not more feeds.
		name := filepath.Base(dir)
		l.dirs[name] = append(l.dirs[name], dir)
		return nil
	}
	for _, s := range subs {
		if err := l.walk(s); err != nil {
			return err
		}
	}
	return nil
}

// Dir returns the directory hr should read and write for the named
// feed. An existing directory wins wherever it sits in the tree;
// otherwise feeds/<group>/<name> is returned without being created.
func (l *FeedLocator) Dir(name, group string) (string, error) {
	hits := l.dirs[name]
	if len(hits) > 1 {
		sort.Strings(hits)
		shown := make([]string, len(hits))
		for i, h := range hits {
			shown[i] = l.rel(h)
		}
		return "", fmt.Errorf(
			"feed %q is claimed by %d directories: %s "+
				"(merge or rename all but one)",
			name, len(hits), strings.Join(shown, ", "))
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	g, err := CleanGroup(group)
	if err != nil {
		return "", fmt.Errorf("feed %q: %w", name, err)
	}
	return filepath.Join(l.feedsDir, filepath.FromSlash(g), name), nil
}

// Names returns every feed directory name found, sorted.
func (l *FeedLocator) Names() []string {
	names := make([]string, 0, len(l.dirs))
	for n := range l.dirs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// All returns every feed directory found, keyed by name. A name with
// more than one path is a collision for the caller to report.
func (l *FeedLocator) All() map[string][]string { return l.dirs }

// FeedDir resolves a single feed's directory. Callers touching many
// feeds should use Locate once instead.
func (v *Vault) FeedDir(name, group string) (string, error) {
	l, err := v.Locate()
	if err != nil {
		return "", err
	}
	return l.Dir(name, group)
}

// CleanGroup normalizes a group — a slash-separated directory path
// relative to feeds/ — and rejects anything escaping the feeds
// directory. "", ".", and "/" all mean the feeds root.
func CleanGroup(group string) (string, error) {
	g := strings.Trim(strings.TrimSpace(group), "/")
	if g == "" || g == "." {
		return "", nil
	}
	clean := path.Clean(g)
	if clean == "." {
		return "", nil
	}
	if path.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf(
			"group %q escapes the feeds directory", group)
	}
	return clean, nil
}

// GroupOf returns the group a feed directory sits in: its path relative
// to feeds/, minus the feed's own name. The feeds root is "".
func (v *Vault) GroupOf(feedDir string) string {
	rel, err := filepath.Rel(v.FeedsDir(), feedDir)
	if err != nil {
		return ""
	}
	group := path.Dir(filepath.ToSlash(rel))
	if group == "." || group == "/" {
		return ""
	}
	return group
}
