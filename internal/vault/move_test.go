package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func moveOne(t *testing.T, v *Vault, name, group string) Move {
	t.Helper()
	moves, err := v.PlanMoves([]string{name}, group)
	if err != nil {
		t.Fatalf("PlanMoves(%q, %q): %v", name, group, err)
	}
	if len(moves) != 1 {
		t.Fatalf("got %d moves, want 1", len(moves))
	}
	if err := v.ApplyMove(moves[0]); err != nil {
		t.Fatalf("ApplyMove: %v", err)
	}
	return moves[0]
}

func TestMoveIntoGroup(t *testing.T) {
	v := newVault(t)
	src := mkfeed(t, v, "matklad")

	m := moveOne(t, v, "matklad", "humans")

	want := filepath.Join(v.FeedsDir(), "humans", "matklad")
	if m.To != want {
		t.Errorf("To = %q, want %q", m.To, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source directory still exists")
	}
	if _, err := os.Stat(filepath.Join(want, "2020-01-01-x-aaaaaaaa.md")); err != nil {
		t.Errorf("article did not come along: %v", err)
	}
	// The feed is findable at its new home.
	l, _ := v.Locate()
	got, err := l.Dir("matklad", "")
	if err != nil || got != want {
		t.Errorf("Dir after move = %q, %v; want %q", got, err, want)
	}
}

func TestMoveToNestedGroupAndBackToRoot(t *testing.T) {
	v := newVault(t)
	mkfeed(t, v, "knuth")

	m := moveOne(t, v, "knuth", "books/classics")
	if want := filepath.Join(v.FeedsDir(), "books", "classics", "knuth"); m.To != want {
		t.Fatalf("To = %q, want %q", m.To, want)
	}

	// "/" means the feeds root.
	back := moveOne(t, v, "knuth", "/")
	if want := filepath.Join(v.FeedsDir(), "knuth"); back.To != want {
		t.Errorf("To = %q, want %q", back.To, want)
	}
	// The emptied books/ and books/classics/ are pruned.
	if _, err := os.Stat(filepath.Join(v.FeedsDir(), "books")); !os.IsNotExist(err) {
		t.Error("empty group directory books/ was left behind")
	}
}

// A move must not prune a group that still holds another feed.
func TestMovePrunesOnlyEmptyGroups(t *testing.T) {
	v := newVault(t)
	mkfeed(t, v, "humans/matklad")
	stay := mkfeed(t, v, "humans/danluu")

	moveOne(t, v, "matklad", "sites")

	if _, err := os.Stat(stay); err != nil {
		t.Errorf("sibling feed disappeared: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.FeedsDir(), "humans")); err != nil {
		t.Errorf("non-empty group was pruned: %v", err)
	}
}

// Moving a feed to where it already is reports a no-op, not an error.
func TestMoveNoOp(t *testing.T) {
	v := newVault(t)
	mkfeed(t, v, "humans/matklad")

	moves, err := v.PlanMoves([]string{"matklad"}, "humans")
	if err != nil {
		t.Fatal(err)
	}
	if !moves[0].NoOp() {
		t.Errorf("want NoOp, got %+v", moves[0])
	}
	if err := v.ApplyMove(moves[0]); err != nil {
		t.Errorf("ApplyMove on a no-op: %v", err)
	}
	if _, err := os.Stat(moves[0].From); err != nil {
		t.Errorf("no-op move disturbed the directory: %v", err)
	}
}

// The whole batch is validated before anything moves, so a bad name in
// the middle leaves the vault untouched.
func TestPlanMovesValidatesWholeBatch(t *testing.T) {
	v := newVault(t)
	a := mkfeed(t, v, "matklad")

	if _, err := v.PlanMoves([]string{"matklad", "nosuchfeed"}, "humans"); err == nil {
		t.Fatal("want an error for an unknown feed")
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("planning moved something: %v", err)
	}
}

func TestPlanMovesRejects(t *testing.T) {
	t.Run("occupied destination", func(t *testing.T) {
		v := newVault(t)
		mkfeed(t, v, "matklad")
		mkfeed(t, v, "humans/matklad")
		// Two dirs share the name, so even resolving it is ambiguous.
		if _, err := v.PlanMoves([]string{"matklad"}, "humans"); err == nil {
			t.Error("want an error")
		}
	})

	t.Run("destination taken by another feed's dir", func(t *testing.T) {
		v := newVault(t)
		mkfeed(t, v, "matklad")
		// A stray non-feed directory already sitting at the target.
		blocked := filepath.Join(v.FeedsDir(), "humans", "matklad")
		if err := os.MkdirAll(blocked, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := v.PlanMoves([]string{"matklad"}, "humans"); err == nil {
			t.Error("want an error for an occupied destination")
		}
	})

	t.Run("escaping group", func(t *testing.T) {
		v := newVault(t)
		mkfeed(t, v, "matklad")
		if _, err := v.PlanMoves([]string{"matklad"}, "../../etc"); err == nil {
			t.Error("want an error for a group escaping feeds/")
		}
	})

	t.Run("feed with no directory", func(t *testing.T) {
		v := newVault(t)
		if _, err := v.PlanMoves([]string{"ghost"}, "humans"); err == nil {
			t.Error("want an error for a feed with no directory")
		}
	})

	t.Run("two feeds colliding on one destination", func(t *testing.T) {
		v := newVault(t)
		mkfeed(t, v, "matklad")
		// Same name twice in one command.
		if _, err := v.PlanMoves([]string{"matklad", "matklad"}, "humans"); err == nil {
			t.Error("want an error for a duplicated destination")
		}
	})
}

func TestFeedNameFromArg(t *testing.T) {
	cases := map[string]string{
		"matklad":               "matklad",
		"feeds/matklad":         "matklad",
		"feeds/humans/matklad":  "matklad",
		"feeds/humans/matklad/": "matklad",
		"  matklad  ":           "matklad",
		"":                      "",
		"/":                     "",
	}
	for in, want := range cases {
		if got := FeedNameFromArg(in); got != want {
			t.Errorf("FeedNameFromArg(%q) = %q, want %q", in, got, want)
		}
	}
}
