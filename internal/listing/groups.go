package listing

import (
	"sort"

	"github.com/isdg/hr/internal/vault"
)

// Group summarizes one folder under feeds/. Counts are for the group's
// own feeds only, not its subgroups, so the numbers sum to the vault
// total rather than double-counting nested shelves.
type Group struct {
	Name     string `json:"name"` // "" is the feeds root
	Feeds    int    `json:"feeds"`
	Articles int    `json:"articles"`
	Unread   int    `json:"unread"`
}

// Groups returns every group that holds at least one feed, sorted by
// name with the feeds root first.
func Groups(v *vault.Vault) ([]Group, error) {
	loc, err := v.Locate()
	if err != nil {
		return nil, err
	}
	byName := map[string]*Group{}
	get := func(name string) *Group {
		g, ok := byName[name]
		if !ok {
			g = &Group{Name: name}
			byName[name] = g
		}
		return g
	}
	for _, paths := range loc.All() {
		for _, p := range paths {
			get(v.GroupOf(p)).Feeds++
		}
	}

	items, err := loadAll(v)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		g := get(it.Group)
		g.Articles++
		if !it.Read {
			g.Unread++
		}
	}

	out := make([]Group, 0, len(byName))
	for _, g := range byName {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
