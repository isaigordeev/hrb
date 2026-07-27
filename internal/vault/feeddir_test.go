package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// mkfeed creates a feed directory at the given feeds-relative path with
// one article file in it, so the locator classifies it as a feed rather
// than a group.
func mkfeed(t *testing.T, v *Vault, rel string) string {
	t.Helper()
	dir := filepath.Join(v.FeedsDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "2020-01-01-x-aaaaaaaa.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newVault(t *testing.T) *Vault {
	t.Helper()
	return &Vault{Root: t.TempDir()}
}

// A feed's directory is found wherever it sits under feeds/, so
// grouping is done by moving directories and needs no config change.
func TestLocateFindsNestedFeedDirs(t *testing.T) {
	v := newVault(t)
	flat := mkfeed(t, v, "lobsters")
	nested := mkfeed(t, v, "humans/matklad")
	deep := mkfeed(t, v, "books/classics/knuth")

	l, err := v.Locate()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, want string }{
		{"lobsters", flat},
		{"matklad", nested},
		{"knuth", deep},
	} {
		got, err := l.Dir(tc.name, "")
		if err != nil {
			t.Fatalf("Dir(%q): %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("Dir(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	names := l.Names()
	if len(names) != 3 {
		t.Errorf("Names() = %v, want 3 feeds", names)
	}
}

// An existing directory wins over the configured group: once a feed has
// been moved, sync must keep writing where it now lives instead of
// re-creating the old location.
func TestDirPrefersExistingOverGroup(t *testing.T) {
	v := newVault(t)
	moved := mkfeed(t, v, "humans/matklad")

	l, err := v.Locate()
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Dir("matklad", "sites")
	if err != nil {
		t.Fatal(err)
	}
	if got != moved {
		t.Errorf("Dir = %q, want existing dir %q", got, moved)
	}
}

// A feed with no directory yet is placed under its configured group.
func TestDirPlacesNewFeedInGroup(t *testing.T) {
	v := newVault(t)
	l, err := v.Locate()
	if err != nil {
		t.Fatal(err)
	}

	got, err := l.Dir("newfeed", "sites/aggregators")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(v.FeedsDir(), "sites", "aggregators", "newfeed")
	if got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Error("Dir must not create the directory")
	}

	got, err = l.Dir("ungrouped", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(v.FeedsDir(), "ungrouped"); got != want {
		t.Errorf("ungrouped Dir = %q, want %q", got, want)
	}
}

// Two directories with the same name are ambiguous: sync must refuse
// rather than silently pick one and split the feed's articles.
func TestDirRejectsDuplicateNames(t *testing.T) {
	v := newVault(t)
	mkfeed(t, v, "humans/matklad")
	mkfeed(t, v, "sites/matklad")

	l, err := v.Locate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Dir("matklad", ""); err == nil {
		t.Fatal("want an error for a name claimed by two directories")
	}
}

// A directory holding only subdirectories is a group, not a feed.
func TestGroupDirsAreNotFeeds(t *testing.T) {
	v := newVault(t)
	mkfeed(t, v, "humans/matklad")

	l, err := v.Locate()
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Names(); len(got) != 1 || got[0] != "matklad" {
		t.Errorf("Names() = %v, want [matklad] (humans is a group)", got)
	}
}

// A missing feeds/ directory is an empty vault, not a failure.
func TestLocateMissingFeedsDir(t *testing.T) {
	v := newVault(t)
	l, err := v.Locate()
	if err != nil {
		t.Fatalf("Locate on vault with no feeds/: %v", err)
	}
	if got := l.Names(); len(got) != 0 {
		t.Errorf("Names() = %v, want none", got)
	}
}

func TestCleanGroup(t *testing.T) {
	ok := map[string]string{
		"":                   "",
		".":                  "",
		"/":                  "",
		"  humans  ":         "humans",
		"/humans/":           "humans",
		"sites/aggregators":  "sites/aggregators",
		"books//classics":    "books/classics",
		"humans/./matklad":   "humans/matklad",
		"humans/x/../people": "humans/people",
	}
	for in, want := range ok {
		got, err := CleanGroup(in)
		if err != nil {
			t.Errorf("CleanGroup(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("CleanGroup(%q) = %q, want %q", in, got, want)
		}
	}

	for _, in := range []string{"..", "../x", "/../x", "a/../.."} {
		if got, err := CleanGroup(in); err == nil {
			t.Errorf("CleanGroup(%q) = %q, want an escape error", in, got)
		}
	}
}

func TestGroupOf(t *testing.T) {
	v := newVault(t)
	cases := map[string]string{
		"lobsters":             "",
		"humans/matklad":       "humans",
		"books/classics/knuth": "books/classics",
	}
	for rel, want := range cases {
		dir := filepath.Join(v.FeedsDir(), filepath.FromSlash(rel))
		if got := v.GroupOf(dir); got != want {
			t.Errorf("GroupOf(%q) = %q, want %q", rel, got, want)
		}
	}
}
