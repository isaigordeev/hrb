package listing

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/isdg/hr/internal/meta"
	"github.com/isdg/hr/internal/vault"
)

// indexVersion bumps whenever the cached Item shape changes, forcing a
// full rebuild on the next list.
const indexVersion = 4

type indexEntry struct {
	MDMod   int64 `json:"md"`   // .md mod time, UnixNano
	MetaMod int64 `json:"meta"` // .meta.toml mod time (0 if absent)
	Item    Item  `json:"item"`
}

type index struct {
	Version int                   `json:"version"`
	Entries map[string]indexEntry `json:"entries"`
}

// loadAll returns every article in the vault as an Item, reusing cached
// entries whose .md and .meta.toml are both unchanged since the last
// list. Results are unfiltered and unsorted. The index is rewritten
// only when something was added, refreshed, or dropped.
func loadAll(v *vault.Vault) ([]Item, error) {
	idxPath := v.IndexPath()
	old := loadIndex(idxPath)
	next := &index{
		Version: indexVersion,
		Entries: make(map[string]indexEntry, len(old.Entries)),
	}

	// One walk gathers mod times for every file (articles and their
	// .meta.toml sidecars alike), so we never stat a path twice.
	mod := map[string]int64{}
	var mdPaths []string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		mod[path] = info.ModTime().UnixNano()
		if strings.HasSuffix(path, ".md") {
			mdPaths = append(mdPaths, path)
		}
		return nil
	}
	if err := filepath.WalkDir(v.FeedsDir(), walk); err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(mdPaths))
	dirty := len(old.Entries) != len(mdPaths)
	for _, p := range mdPaths {
		mdMod := mod[p]
		metaMod := mod[meta.Path(p)] // 0 when sidecar is absent
		if e, ok := old.Entries[p]; ok &&
			e.MDMod == mdMod && e.MetaMod == metaMod {
			next.Entries[p] = e
			items = append(items, e.Item)
			continue
		}
		it, err := loadItem(p, v.GroupOf(filepath.Dir(p)))
		if err != nil {
			dirty = true
			continue
		}
		next.Entries[p] = indexEntry{
			MDMod:   mdMod,
			MetaMod: metaMod,
			Item:    it,
		}
		items = append(items, it)
		dirty = true
	}

	if dirty {
		// Best effort: a failed write just rebuilds next time.
		_ = saveIndex(idxPath, next)
	}
	return items, nil
}

// Stat summarizes the on-disk listing cache for `hr index`. It never
// modifies the index.
type Stat struct {
	Path        string // index file location
	Exists      bool   // index file is present
	Version     int    // version stored on disk (0 if missing/corrupt)
	WantVersion int    // version the current binary expects
	Entries     int    // cached article count
}

// Stale reports whether the next list will discard and rebuild the
// cache (missing, corrupt, or a version the binary no longer accepts).
func (s Stat) Stale() bool { return !s.Exists || s.Version != s.WantVersion }

// StatIndex reports the state of the on-disk listing index without
// touching it.
func StatIndex(v *vault.Vault) Stat {
	s := Stat{Path: v.IndexPath(), WantVersion: indexVersion}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return s
	}
	s.Exists = true
	var d index
	if json.Unmarshal(data, &d) != nil {
		return s // present but corrupt: Version stays 0
	}
	s.Version = d.Version
	s.Entries = len(d.Entries)
	return s
}

// ClearIndex removes the on-disk listing cache. A missing file is not an
// error.
func ClearIndex(v *vault.Vault) error {
	err := os.Remove(v.IndexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RebuildIndex discards the cache and re-parses every article from disk,
// returning the number of articles indexed.
func RebuildIndex(v *vault.Vault) (int, error) {
	if err := ClearIndex(v); err != nil {
		return 0, err
	}
	items, err := loadAll(v)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// loadIndex reads the on-disk index, returning a fresh empty index if
// it is missing, unreadable, corrupt, or from an older version.
func loadIndex(path string) *index {
	idx := &index{Version: indexVersion, Entries: map[string]indexEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return idx
	}
	var d index
	if json.Unmarshal(data, &d) != nil || d.Version != indexVersion {
		return idx
	}
	if d.Entries == nil {
		d.Entries = map[string]indexEntry{}
	}
	return &d
}

func saveIndex(path string, idx *index) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
