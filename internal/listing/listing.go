// Package listing scans a vault and returns articles with their
// metadata for human/machine display.
package listing

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/isdg/hr/internal/article"
	"github.com/isdg/hr/internal/meta"
	"github.com/isdg/hr/internal/textfmt"
	"github.com/isdg/hr/internal/vault"
)

type Item struct {
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	Alias     string   `json:"alias,omitempty"`
	URL       string   `json:"url"`
	Feed      string   `json:"feed"`
	Group     string   `json:"group,omitempty"`
	GUID      string   `json:"guid,omitempty"`
	Published PubTime  `json:"published"`
	Read      bool     `json:"read"`
	Favorite  bool     `json:"favorite"`
	Tags      []string `json:"tags,omitempty"`
	Corrupt   int      `json:"corrupt,omitempty"` // count of corruption marks
}

// PubTime wraps time.Time so it JSON-serializes via an RFC3339 string
// that tolerates BC (negative) years. time.Time's own MarshalJSON errors
// for any year outside [0,9999], which would otherwise break `hr list
// --json` and the listing cache for BC-dated articles.
type PubTime struct{ time.Time }

func (t PubTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Format(time.RFC3339))
}

func (t *PubTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	t.Time = parsePublished(s)
	return nil
}

// Label returns the alias if set, otherwise the article title.
func (it Item) Label() string {
	if it.Alias != "" {
		return it.Alias
	}
	return it.Title
}

type Filter struct {
	Feeds     []string // any-of; empty means every feed
	Groups    []string // any-of; empty means every group
	Tag       string
	Unread    bool
	Corrupted bool
	Since     time.Duration
}

func List(v *vault.Vault, f Filter) ([]Item, error) {
	all, err := loadAll(v)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	items := make([]Item, 0, len(all))
	for _, it := range all {
		if f.match(it, now) {
			items = append(items, it)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Published.After(items[j].Published.Time)
	})
	return items, nil
}

func loadItem(path, group string) (Item, error) {
	fm, err := article.ParseFile(path)
	if err != nil {
		return Item{}, err
	}
	m := meta.LoadOrDefault(path)
	pub := parsePublished(fm.Published)
	return Item{
		Path:      path,
		Title:     textfmt.Line(fm.Title),
		Alias:     textfmt.Line(m.Alias),
		URL:       textfmt.Line(fm.URL),
		Feed:      textfmt.Line(fm.Feed),
		Group:     group,
		GUID:      textfmt.Line(fm.GUID),
		Published: PubTime{pub},
		Read:      m.Read,
		Favorite:  m.Favorite,
		Tags:      m.Tags,
		Corrupt:   len(m.Corruptions),
	}, nil
}

// parsePublished parses an RFC3339 timestamp, tolerating the signed
// negative years (e.g. "-0350-07-03T00:00:00Z") that hr writes for
// BC-dated articles but that Go's RFC3339 layout refuses to parse back.
// Returns the zero time if the value is unparseable.
func parsePublished(s string) time.Time {
	return article.ParseTime(s)
}

func (f Filter) match(it Item, now time.Time) bool {
	if !f.matchFeed(it) {
		return false
	}
	if f.Tag != "" && !slices.Contains(it.Tags, f.Tag) {
		return false
	}
	if !f.matchGroup(it) {
		return false
	}
	if f.Unread && it.Read {
		return false
	}
	if f.Corrupted && it.Corrupt == 0 {
		return false
	}
	if f.Since > 0 && it.Published.Before(now.Add(-f.Since)) {
		return false
	}
	return true
}

// matchFeed reports whether an item comes from any of the filter's
// feeds. Feeds stack as a union, so --feed matklad --feed danluu reads
// both authors. Matching is exact: a feed is a name, not a path or a
// prefix.
func (f Filter) matchFeed(it Item) bool {
	if len(f.Feeds) == 0 {
		return true
	}
	return slices.Contains(f.Feeds, it.Feed)
}

// matchGroup reports whether an item falls under any of the filter's
// groups. Groups stack as a union, so --group humans --group sites
// reads both shelves.
//
// Each match is by subtree, so --group humans also covers the feeds in
// humans/archive: the folder you name is the shelf you asked for, not
// one exact level of it.
func (f Filter) matchGroup(it Item) bool {
	if len(f.Groups) == 0 {
		return true
	}
	for _, raw := range f.Groups {
		g, err := vault.CleanGroup(raw)
		if err != nil {
			continue
		}
		if g == "" {
			if it.Group == "" { // an explicit "/" means the feeds root only
				return true
			}
			continue
		}
		if it.Group == g || strings.HasPrefix(it.Group, g+"/") {
			return true
		}
	}
	return false
}

// ParseSince accepts standard Go durations (e.g. 24h) plus the "Nd"
// shorthand for N days.
func ParseSince(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("invalid days: %w", err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
