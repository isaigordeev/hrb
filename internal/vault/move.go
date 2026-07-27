package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Move is one feed directory relocation: an existing directory and the
// path it should occupy after the move.
type Move struct {
	Name string
	From string // absolute, must exist
	To   string // absolute, must not exist unless equal to From
}

// NoOp reports whether the feed is already where it was asked to go.
func (m Move) NoOp() bool { return m.From == m.To }

// PlanMoves resolves each named feed to its current directory and the
// one it would occupy in group, validating the entire batch before the
// caller touches anything. Names may be given bare ("matklad") or as a
// vault path ("feeds/humans/matklad"); only the final element is used,
// since a feed is identified by its directory name.
func (v *Vault) PlanMoves(names []string, group string) ([]Move, error) {
	g, err := CleanGroup(group)
	if err != nil {
		return nil, err
	}
	loc, err := v.Locate()
	if err != nil {
		return nil, err
	}

	moves := make([]Move, 0, len(names))
	targets := map[string]string{} // destination -> feed that claimed it
	for _, raw := range names {
		name := FeedNameFromArg(raw)
		if name == "" {
			return nil, fmt.Errorf("empty feed name in %q", raw)
		}
		from, err := loc.Dir(name, "")
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(from); err != nil {
			return nil, fmt.Errorf(
				"feed %q has no directory under feeds/ "+
					"(nothing to move; `hr sync` creates it)", name)
		}

		to := filepath.Join(v.FeedsDir(), filepath.FromSlash(g), name)
		if other, dup := targets[to]; dup {
			return nil, fmt.Errorf(
				"feeds %q and %q would both become %s",
				other, name, v.Rel(to))
		}
		targets[to] = name

		if from != to {
			if _, err := os.Stat(to); err == nil {
				return nil, fmt.Errorf(
					"%s already exists (move or remove it first)", v.Rel(to))
			}
			if within(to, from) {
				return nil, fmt.Errorf(
					"cannot move %s into itself", v.Rel(from))
			}
		}
		moves = append(moves, Move{Name: name, From: from, To: to})
	}
	return moves, nil
}

// ApplyMove renames the feed directory and prunes any group directories
// the move leaves empty, so an emptied folder doesn't linger in the
// tree. A no-op move does nothing.
func (v *Vault) ApplyMove(m Move) error {
	if m.NoOp() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.To), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", v.Rel(filepath.Dir(m.To)), err)
	}
	if err := os.Rename(m.From, m.To); err != nil {
		return fmt.Errorf("move %s: %w", v.Rel(m.From), err)
	}
	v.pruneEmptyGroups(filepath.Dir(m.From))
	return nil
}

// pruneEmptyGroups removes now-empty group directories from dir upward,
// stopping at feeds/. Best effort: a non-empty or undeletable directory
// just ends the walk.
func (v *Vault) pruneEmptyGroups(dir string) {
	feeds := v.FeedsDir()
	for dir != feeds && within(dir, feeds) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// FeedNameFromArg reduces a command-line argument to a feed name: a
// bare name is returned as-is, and a path ("feeds/humans/matklad", or a
// shell-completed "feeds/humans/matklad/") contributes its last element.
func FeedNameFromArg(arg string) string {
	s := strings.TrimSpace(arg)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(s))
}

// within reports whether path is inside (or equal to) root.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
