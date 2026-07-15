// Package cache persists per-feed sync state (ETag, Last-Modified,
// last fetch time) to .hr/cache.json. It is gitignored.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

type Cache struct {
	path    string
	mu      sync.Mutex
	Entries map[string]Entry `json:"entries"`
}

func Load(path string) (*Cache, error) {
	c := &Cache{path: path, Entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	if c.Entries == nil {
		c.Entries = map[string]Entry{}
	}
	return c, nil
}

func (c *Cache) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

// Get and Set are safe for concurrent use so the syncer can update
// per-feed state from multiple workers. Load and Save are not guarded;
// they run single-threaded before and after the worker pool.
func (c *Cache) Get(feed string) Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Entries[feed]
}

func (c *Cache) Set(feed string, e Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Entries[feed] = e
}
