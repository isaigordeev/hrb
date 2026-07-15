package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/isdg/hr/internal/cache"
	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/vault"
)

// testServer serves three routes: /ok returns a 200 feed with two items
// and an ETag, /notmod always replies 304, and /err always fails with
// 500.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		fmtFeed(w, "ok")
	})
	mux.HandleFunc("/ok2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v2"`)
		fmtFeed(w, "ok2")
	})
	mux.HandleFunc("/notmod", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("/err", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	return httptest.NewServer(mux)
}

func fmtFeed(w http.ResponseWriter, name string) {
	body := "<?xml version=\"1.0\"?>\n<rss version=\"2.0\"><channel>\n" +
		"<title>" + name + "</title>\n" +
		"<item><title>First " + name + "</title>" +
		"<link>http://example.com/" + name + "/1</link>" +
		"<guid>" + name + "-1</guid></item>\n" +
		"<item><title>Second " + name + "</title>" +
		"<link>http://example.com/" + name + "/2</link>" +
		"<guid>" + name + "-2</guid></item>\n" +
		"</channel></rss>"
	_, _ = w.Write([]byte(body))
}

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	v := &vault.Vault{Root: t.TempDir()}
	for _, d := range []string{v.FeedsDir(), v.MetaDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	return v
}

// TestRunConcurrent verifies that concurrent sync produces correct,
// deterministically-ordered results across a mix of feed outcomes and
// updates the cache without data races (run with -race). It runs at
// several concurrency levels to cover both the sequential and pooled
// paths.
func TestRunConcurrent(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()

	feeds := []config.Feed{
		{Name: "alpha", URL: srv.URL + "/ok"},
		{Name: "bravo", URL: srv.URL + "/notmod"},
		{Name: "charlie", URL: srv.URL + "/err"},
		{Name: "delta", URL: srv.URL + "/ok2"},
	}

	for _, jobs := range []int{1, 2, 8} {
		t.Run("jobs="+strconv.Itoa(jobs), func(t *testing.T) {
			v := newVault(t)
			res, err := Run(context.Background(), Options{
				Vault:       v,
				Config:      &config.Config{Feeds: feeds},
				UserAgent:   "hr-test",
				Concurrency: jobs,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			// Result order matches config order regardless of completion.
			if len(res.Feeds) != len(feeds) {
				t.Fatalf("got %d feeds, want %d", len(res.Feeds), len(feeds))
			}
			for i, want := range feeds {
				if res.Feeds[i].Name != want.Name {
					t.Errorf("feed[%d] = %q, want %q (order not preserved)",
						i, res.Feeds[i].Name, want.Name)
				}
			}

			alpha, bravo, charlie, delta := res.Feeds[0], res.Feeds[1],
				res.Feeds[2], res.Feeds[3]
			if alpha.New != 2 || alpha.Existing != 0 || alpha.Err != nil {
				t.Errorf("alpha = %+v, want 2 new / 0 existing / no err", alpha)
			}
			if !bravo.NotModified || bravo.Err != nil {
				t.Errorf("bravo = %+v, want NotModified", bravo)
			}
			if charlie.Err == nil {
				t.Errorf("charlie = %+v, want an error", charlie)
			}
			if delta.New != 2 || delta.Err != nil {
				t.Errorf("delta = %+v, want 2 new / no err", delta)
			}

			// Cache holds entries for the two modified feeds.
			c, err := cache.Load(v.CachePath())
			if err != nil {
				t.Fatalf("load cache: %v", err)
			}
			if got := c.Get("alpha").ETag; got != `"v1"` {
				t.Errorf("alpha ETag = %q, want \"v1\"", got)
			}
			if c.Get("alpha").FetchedAt.IsZero() {
				t.Errorf("alpha FetchedAt not set")
			}

			// Articles landed on disk under their feed dir.
			hits, _ := filepath.Glob(
				filepath.Join(v.FeedsDir(), "alpha", "*.md"))
			if len(hits) != 2 {
				t.Errorf("alpha articles on disk = %d, want 2", len(hits))
			}
		})
	}
}

// TestRunNoFeeds guards the empty-feed-list edge (worker count clamps to
// zero without deadlocking).
func TestRunNoFeeds(t *testing.T) {
	v := newVault(t)
	res, err := Run(context.Background(), Options{
		Vault:  v,
		Config: &config.Config{Feeds: nil},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Feeds) != 0 {
		t.Fatalf("got %d feeds, want 0", len(res.Feeds))
	}
}
