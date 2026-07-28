// Package doctor checks a vault's config and filesystem for
// inconsistencies that hr's normal commands don't otherwise surface:
// malformed sidecars, orphaned files, tombstone contradictions, id
// collisions, and config/directory drift.
package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/isdg/hr/internal/article"
	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/meta"
	"github.com/isdg/hr/internal/tombstone"
	"github.com/isdg/hr/internal/vault"
)

type Severity string

const (
	Error Severity = "error"
	Warn  Severity = "warn"
	Info  Severity = "info"
)

type Issue struct {
	Severity Severity
	Path     string // vault-relative; "" for vault-wide issues
	Message  string
}

type Report struct {
	Issues []Issue
}

func (r Report) HasErrors() bool {
	for _, i := range r.Issues {
		if i.Severity == Error {
			return true
		}
	}
	return false
}

func (r Report) Count(sev Severity) int {
	n := 0
	for _, i := range r.Issues {
		if i.Severity == sev {
			n++
		}
	}
	return n
}

// Check inspects the vault's config and every feed directory, returning
// every issue found. It never mutates the vault.
func Check(v *vault.Vault, cfg *config.Config) (Report, error) {
	var r Report
	add := func(sev Severity, path, format string, args ...any) {
		r.Issues = append(r.Issues, Issue{sev, path, fmt.Sprintf(format, args...)})
	}

	feedNames := checkConfig(cfg, add)
	checkGitignore(v, add)

	// Feed directories may be grouped under feeds/ at any depth, so ask
	// the vault where they are rather than assuming one flat level.
	loc, err := v.Locate()
	if err != nil {
		return r, fmt.Errorf("read feeds dir: %w", err)
	}
	for _, name := range loc.Names() {
		paths := loc.All()[name]
		if len(paths) > 1 {
			shown := make([]string, len(paths))
			for i, p := range paths {
				shown[i] = v.Rel(p)
			}
			sort.Strings(shown)
			add(Error, "feeds",
				"feed name %q claimed by %d directories: %s",
				name, len(paths), strings.Join(shown, ", "))
		}
		for _, p := range paths {
			rel := v.Rel(p)
			if !feedNames[name] {
				add(Warn, rel,
					"directory not declared in hr.toml (renamed or removed feed?)")
			}
			checkFeedDir(p, name, rel, add)
		}
	}

	sort.SliceStable(r.Issues, func(i, j int) bool {
		if r.Issues[i].Path != r.Issues[j].Path {
			return r.Issues[i].Path < r.Issues[j].Path
		}
		return severityRank(r.Issues[i].Severity) < severityRank(r.Issues[j].Severity)
	})
	return r, nil
}

func severityRank(s Severity) int {
	switch s {
	case Error:
		return 0
	case Warn:
		return 1
	default:
		return 2
	}
}

type addFunc func(sev Severity, path, format string, args ...any)

func checkConfig(cfg *config.Config, add addFunc) map[string]bool {
	names := make(map[string]bool, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		if f.Name == "" {
			add(Error, "hr.toml", "feed with empty name (url %q)", f.URL)
			continue
		}
		if strings.ContainsAny(f.Name, "/\\") {
			add(Error, "hr.toml",
				"feed name %q must not contain a path separator", f.Name)
		}
		if names[f.Name] {
			add(Error, "hr.toml", "duplicate feed name %q", f.Name)
		}
		names[f.Name] = true
		switch {
		case f.IsManual():
			// A hand-curated feed has nothing to poll; a url alongside the
			// tag is contradictory, since sync will never fetch it.
			if f.URL != "" {
				add(Warn, "hr.toml",
					"feed %q is tagged %q but also has a url; sync skips it",
					f.Name, config.TagManual)
			}
		case f.URL == "":
			add(Error, "hr.toml",
				"feed %q has no url (tag it %q if it is hand-curated)",
				f.Name, config.TagManual)
		}
		if _, err := vault.CleanGroup(f.Group); err != nil {
			add(Error, "hr.toml", "feed %q: %v", f.Name, err)
		}
	}
	return names
}

func checkGitignore(v *vault.Vault, add addFunc) {
	data, err := os.ReadFile(filepath.Join(v.Root, ".gitignore"))
	if err != nil || !strings.Contains(string(data), ".hr/") {
		add(Warn, ".gitignore",
			`missing ".hr/" entry — cache/raw HTML archive may get committed`)
	}
}

// checkFeedDir inspects one feed directory. relDir is its vault-relative
// path (e.g. "feeds/humans/matklad"), used for issue paths, while
// feedName is the identity the frontmatter should agree with.
func checkFeedDir(dir, feedName, relDir string, add addFunc) {
	files, err := os.ReadDir(dir)
	if err != nil {
		add(Error, relDir, "read dir: %v", err)
		return
	}

	deletedIDs, _ := tombstone.DeletedIDs(dir)
	mdBases := make(map[string]bool)      // base filename (no .md) -> present
	metaBases := make(map[string]bool)    // base filename (no .meta.toml) -> present
	idOwners := make(map[string][]string) // id -> owning filenames

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		rel := relDir + "/" + name

		switch {
		case strings.HasSuffix(name, ".meta.toml"):
			metaBases[strings.TrimSuffix(name, ".meta.toml")] = true
			articlePath := filepath.Join(dir, strings.TrimSuffix(name, ".meta.toml")+".md")
			if _, err := meta.Load(articlePath); err != nil && !os.IsNotExist(err) {
				add(Error, rel, "malformed sidecar: %v", err)
			}

		case strings.HasSuffix(name, ".deleted"):
			// contradiction check happens after the full scan, once
			// mdBases/ids are known.

		case strings.HasSuffix(name, ".md"):
			mdBases[strings.TrimSuffix(name, ".md")] = true
			checkArticle(dir, name, feedName, rel, idOwners, add)

		default:
			add(Warn, rel, "unexpected file in feed directory")
		}
	}

	for base := range metaBases {
		if !mdBases[base] {
			add(Warn, relDir+"/"+base+".meta.toml",
				"orphaned sidecar: no matching article")
		}
	}

	for id, owners := range idOwners {
		if len(owners) > 1 {
			add(Error, relDir,
				"id %s claimed by multiple files: %s", id, strings.Join(owners, ", "))
		}
	}

	for id := range deletedIDs {
		for base := range mdBases {
			if strings.HasSuffix(base, "-"+id) {
				add(Error, relDir+"/"+base+".md",
					"tombstoned id %s still has a live article file "+
						"(sync will keep skipping it, but it wasn't cleaned up)", id)
			}
		}
	}
}

func checkArticle(
	dir, name, feedName, rel string, idOwners map[string][]string, add addFunc,
) {
	id, ok := article.IDFromName(name)
	if !ok {
		add(Error, rel, "filename doesn't match the canonical <date>-<slug>-<id>.md pattern")
		return
	}
	idOwners[id] = append(idOwners[id], name)

	fm, _, err := article.ReadFile(filepath.Join(dir, name))
	if err != nil {
		add(Error, rel, "unparsable frontmatter: %v", err)
		return
	}
	if fm.Feed != "" && fm.Feed != feedName {
		add(Warn, rel, "frontmatter feed %q doesn't match directory %q", fm.Feed, feedName)
	}

	// Only GUID/URL-derived ids are stable identifiers worth checking:
	// a title+timestamp-derived id legitimately changes if the title is
	// hand-edited, so a mismatch there is expected, not a fault.
	if fm.GUID == "" && fm.URL == "" {
		return
	}
	want := (&article.Article{
		Title:     fm.Title,
		URL:       fm.URL,
		GUID:      fm.GUID,
		Published: article.ParseTime(fm.Published),
	}).ID()
	if want != id {
		add(Warn, rel, "id drift: filename has %s, frontmatter (guid/url) implies %s", id, want)
	}
}
