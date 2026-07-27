package article

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// After `hr edit` renames an article (new date/slug, same id), a
// subsequent sync of the same feed item must dedup by id and not write a
// duplicate under the original name.
func TestWriteDedupByID(t *testing.T) {
	// Write now takes the feed directory itself: the caller (sync) is
	// what resolves where a feed lives under feeds/.
	dir := filepath.Join(t.TempDir(), "demo")
	a := &Article{
		Title:     "Old Title",
		URL:       "http://example.com/x",
		Published: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		FeedName:  "demo",
		GUID:      "http://example.com/x",
		Body:      "hi",
	}

	created, path, err := Write(dir, a)
	if err != nil || !created {
		t.Fatalf("first write: created=%v err=%v", created, err)
	}

	// Simulate `hr edit`: rename to a different date/slug, same id.
	id, ok := IDFromName(filepath.Base(path))
	if !ok {
		t.Fatalf("could not extract id from %q", path)
	}
	newName := NameFor("New Title", time.Date(-350, 1, 1, 0, 0, 0, 0, time.UTC), id)
	renamed := filepath.Join(dir, newName)
	if err := os.Rename(path, renamed); err != nil {
		t.Fatal(err)
	}

	// Re-sync the same feed item: must skip, returning the renamed file.
	created2, p2, err := Write(dir, a)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("re-wrote article after rename; dedup is not id-based")
	}
	if p2 != renamed {
		t.Fatalf("dedup returned %q, want %q", p2, renamed)
	}
	hits, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	if len(hits) != 1 {
		t.Fatalf("want exactly 1 article file, got %d: %v", len(hits), hits)
	}
}
