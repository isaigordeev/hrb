// Package config loads and saves the vault's hr.toml.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Name      string `toml:"name"`
	Feeds     []Feed `toml:"feeds"`
	AutoRead  bool   `toml:"autoread"`
	ShowRead  bool   `toml:"showread"`
	Ordering  string `toml:"ordering"`
	UserAgent string `toml:"user_agent"`
}

// TagManual marks a hand-curated feed: one with nothing to poll, whose
// articles are written by hand (or by a build script) instead of fetched.
// `hr sync` reports such feeds as needing a manual update rather than
// trying to fetch them.
const TagManual = "manual"

type Feed struct {
	// URL is the feed to poll. Required unless the feed is tagged
	// TagManual, in which case there is nothing to poll.
	URL  string   `toml:"url"`
	Name string   `toml:"name"`
	Tags []string `toml:"tags"`

	// Group is the folder under feeds/ this feed belongs to, e.g.
	// "humans" or "sites/aggregators". It only decides where a feed
	// with no directory yet is created: once the directory exists, its
	// location on disk is authoritative and moving it (with `hr mv`,
	// `mv`, or `git mv`) is what regroups the feed.
	Group string `toml:"group"`
}

// IsManual reports whether the feed is hand-curated, i.e. carries the
// TagManual tag.
func (f Feed) IsManual() bool {
	for _, t := range f.Tags {
		if strings.EqualFold(strings.TrimSpace(t), TagManual) {
			return true
		}
	}
	return false
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &c, nil
}
