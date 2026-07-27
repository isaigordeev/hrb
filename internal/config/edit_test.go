package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hr.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The whole point of editing hr.toml as text: a hand-written config is
// mostly comments, and a TOML round-trip would erase all of them.
func TestSetGroupsPreservesCommentsAndFormatting(t *testing.T) {
	// Top-level keys precede the first [[feeds]] block: in TOML, bare
	// keys after an array-of-tables header belong to that table.
	src := `name = "blog"

# Sort order for ` + "`hr list`" + `. "desc" = newest first.
ordering = "desc"
autoread = false

# Alex Kladov — Rust, IDEs, testing.
[[feeds]]
url  = "https://matklad.github.io/feed.xml"
name = "matklad"

# Lobsters — invite-only tech link aggregator.
# Firehose of every new submission.
[[feeds]]
url  = "https://lobste.rs/newest.rss"
name = "lobsters"
`
	p := writeConfig(t, src)

	n, err := SetGroups(p, map[string]string{
		"matklad":  "humans",
		"lobsters": "sites",
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("changed %d blocks, want 2", n)
	}

	got := read(t, p)
	for _, want := range []string{
		"# Alex Kladov — Rust, IDEs, testing.",
		"# Lobsters — invite-only tech link aggregator.",
		"# Firehose of every new submission.",
		"# Sort order for `hr list`. \"desc\" = newest first.",
		`url  = "https://matklad.github.io/feed.xml"`,
		"ordering = \"desc\"",
		"autoread = false",
		`group = "humans"`,
		`group = "sites"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lost %q:\n%s", want, got)
		}
	}

	// group lands inside the right block, right after name.
	if !strings.Contains(got, "name = \"matklad\"\ngroup = \"humans\"") {
		t.Errorf("matklad group misplaced:\n%s", got)
	}
	if !strings.Contains(got, "name = \"lobsters\"\ngroup = \"sites\"") {
		t.Errorf("lobsters group misplaced:\n%s", got)
	}

	// The file must still parse, with everything else intact.
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	if cfg.Name != "blog" || cfg.Ordering != "desc" || cfg.AutoRead {
		t.Errorf("top-level keys changed: %+v", cfg)
	}
	byName := map[string]Feed{}
	for _, f := range cfg.Feeds {
		byName[f.Name] = f
	}
	if got := byName["matklad"].Group; got != "humans" {
		t.Errorf("matklad group = %q, want humans", got)
	}
	if got := byName["matklad"].URL; got != "https://matklad.github.io/feed.xml" {
		t.Errorf("matklad url changed to %q", got)
	}
	if got := byName["lobsters"].Group; got != "sites" {
		t.Errorf("lobsters group = %q, want sites", got)
	}
}

func TestSetGroupsReplacesExistingGroup(t *testing.T) {
	p := writeConfig(t, `[[feeds]]
url  = "https://x.test/f.xml"
name = "x"
group = "old"
tags = ["a"]
`)
	if _, err := SetGroups(p, map[string]string{"x": "new/deeper"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if strings.Contains(got, `"old"`) {
		t.Errorf("old group survived:\n%s", got)
	}
	if !strings.Contains(got, `group = "new/deeper"`) {
		t.Errorf("new group missing:\n%s", got)
	}
	if !strings.Contains(got, `tags = ["a"]`) {
		t.Errorf("sibling key lost:\n%s", got)
	}
}

// Moving a feed back to the feeds root drops the key rather than
// writing an empty string.
func TestSetGroupsEmptyRemovesKey(t *testing.T) {
	p := writeConfig(t, `[[feeds]]
url  = "https://x.test/f.xml"
name = "x"
group = "sites"
`)
	n, err := SetGroups(p, map[string]string{"x": ""})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("changed %d, want 1", n)
	}
	if got := read(t, p); strings.Contains(got, "group") {
		t.Errorf("group key survived:\n%s", got)
	}
}

// A no-op edit must not rewrite the file at all.
func TestSetGroupsNoChangeLeavesFileAlone(t *testing.T) {
	src := `[[feeds]]
url  = "https://x.test/f.xml"
name = "x"
group = "sites"
`
	p := writeConfig(t, src)
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	// Same value, an unknown feed, and a feed that never had the key.
	for _, m := range []map[string]string{
		{"x": "sites"},
		{"nosuchfeed": "humans"},
	} {
		n, err := SetGroups(p, m)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("SetGroups(%v) changed %d blocks, want 0", m, n)
		}
	}
	if got := read(t, p); got != src {
		t.Errorf("file rewritten:\n%s", got)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("file mtime changed on a no-op edit")
	}
}

// A [[feeds]] block ends at the next table header, so a group is never
// injected into whatever follows the feed list.
func TestSetGroupsRespectsBlockBoundaries(t *testing.T) {
	p := writeConfig(t, `[[feeds]]
url  = "https://x.test/f.xml"
name = "x"

[[feeds]]
url  = "https://y.test/f.xml"
name = "y"

[other]
name = "x"
`)
	if _, err := SetGroups(p, map[string]string{"x": "g"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if n := strings.Count(got, `group = "g"`); n != 1 {
		t.Errorf("group written %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "[other]\nname = \"x\"\n") {
		t.Errorf("unrelated table touched:\n%s", got)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	if len(cfg.Feeds) != 2 {
		t.Errorf("feed count = %d, want 2", len(cfg.Feeds))
	}
}

func TestSetGroupsHandlesSpacingAndComments(t *testing.T) {
	p := writeConfig(t, `[[ feeds ]]
  url   =  "https://x.test/f.xml"
  name  =  "x"   # the feed
`)
	if _, err := SetGroups(p, map[string]string{"x": "g"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	if len(cfg.Feeds) != 1 || cfg.Feeds[0].Group != "g" {
		t.Errorf("feeds = %+v, want one feed grouped as g", cfg.Feeds)
	}
}

func TestSetGroupsNoTrailingNewlinePreserved(t *testing.T) {
	p := writeConfig(t, "[[feeds]]\nurl  = \"https://x.test/f.xml\"\nname = \"x\"")
	if _, err := SetGroups(p, map[string]string{"x": "g"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); strings.HasSuffix(got, "\n") {
		t.Errorf("gained a trailing newline: %q", got)
	}
}

// Trailing keys after the last [[feeds]] block belong to that feed's
// table in TOML, so the block extends to EOF and a group is inserted
// after `name` rather than at the very end. (Whether those keys were
// *meant* to be top-level is a separate question; SetGroups must not
// change their meaning either way.)
func TestSetGroupsWithKeysTrailingTheLastBlock(t *testing.T) {
	p := writeConfig(t, `[[feeds]]
url  = "https://lobste.rs/newest.rss"
name = "lobsters"

# Sort order.
ordering = "desc"
user_agent = "hr/0.1"
`)
	if _, err := SetGroups(p, map[string]string{"lobsters": "sites"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.Contains(got, "name = \"lobsters\"\ngroup = \"sites\"") {
		t.Errorf("group misplaced:\n%s", got)
	}
	for _, want := range []string{"# Sort order.", `ordering = "desc"`, `user_agent = "hr/0.1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	if len(cfg.Feeds) != 1 || cfg.Feeds[0].Group != "sites" {
		t.Errorf("feeds = %+v", cfg.Feeds)
	}
}
