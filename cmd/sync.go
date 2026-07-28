package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/syncer"
	"github.com/isdg/hr/internal/vault"
)

var (
	syncFeedFilter string
	syncForce      bool
	syncJobs       int
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Fetch new items for all (or filtered) feeds",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := vault.Resolve(vaultFlag)
		if err != nil {
			return err
		}
		v, err := vault.Open(root)
		if err != nil {
			return err
		}
		cfg, err := config.Load(v.ConfigPath())
		if err != nil {
			return err
		}
		ua := cfg.UserAgent
		if ua == "" {
			ua = "hr/0.1"
		}

		// Live progress → stderr; the résumé → stdout. Color on each is
		// gated on that stream being a terminal.
		live := newStyler(os.Stderr)
		nameW, total := feedLayout(cfg, syncFeedFilter)
		counterW := len(strconv.Itoa(total))

		start := time.Now()
		res, err := syncer.Run(cmd.Context(), syncer.Options{
			Vault:       v,
			Config:      cfg,
			FeedName:    syncFeedFilter,
			UserAgent:   ua,
			Force:       syncForce,
			Concurrency: syncJobs,
			OnFeedDone: func(i, tot int, fr syncer.FeedResult) {
				fmt.Fprintln(os.Stderr,
					liveLine(live, nameW, counterW, i, tot, fr))
			},
		})
		if res != nil {
			printSyncSummary(res, time.Since(start))
		}
		return err
	},
}

// feedLayout returns the widest selected feed name and how many feeds are
// selected, so the live log can align its columns before syncing starts.
func feedLayout(cfg *config.Config, filter string) (nameW, total int) {
	for _, f := range cfg.Feeds {
		if filter != "" && f.Name != filter {
			continue
		}
		if len(f.Name) > nameW {
			nameW = len(f.Name)
		}
		total++
	}
	return nameW, total
}

// liveLine renders one uv-style progress line as a feed finishes:
//
//	26/61  yandex           +3 new
//
// counter dim, name left-padded, status green (+N new) / dim
// (· up to date) / red (✗ error) / yellow (⚠ manual).
func liveLine(
	st styler, nameW, counterW, i, total int, fr syncer.FeedResult,
) string {
	counter := st.dim(fmt.Sprintf("%*d/%d", counterW, i, total))
	var status string
	switch {
	case fr.Err != nil:
		status = st.red("✗ error")
	case fr.Manual:
		status = st.yellow("⚠ manual")
	case fr.New > 0:
		status = st.green(fmt.Sprintf("+%d new", fr.New))
	default:
		status = st.dim("· up to date")
	}
	return fmt.Sprintf("  %s  %-*s  %s", counter, nameW, fr.Name, status)
}

// printSyncSummary writes the résumé to stdout: a one-line header, only
// the feeds that gained new items (most first), any errors, and a footer
// of aggregate counts.
func printSyncSummary(r *syncer.Result, elapsed time.Duration) {
	st := newStyler(os.Stdout)

	var changed, errored, manual []syncer.FeedResult
	var totNew, unchanged, nameW int
	for _, fr := range r.Feeds {
		switch {
		case fr.Err != nil:
			errored = append(errored, fr)
		case fr.Manual:
			manual = append(manual, fr)
			if len(fr.Name) > nameW {
				nameW = len(fr.Name)
			}
		case fr.New > 0:
			changed = append(changed, fr)
			totNew += fr.New
			if len(fr.Name) > nameW {
				nameW = len(fr.Name)
			}
		default:
			unchanged++
		}
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].New != changed[j].New {
			return changed[i].New > changed[j].New
		}
		return changed[i].Name < changed[j].Name
	})

	feeds := "feeds"
	if len(r.Feeds) == 1 {
		feeds = "feed"
	}
	fmt.Printf("%s synced %d %s in %s\n",
		st.bold("✓"), len(r.Feeds), feeds, fmtDur(elapsed))

	if len(changed) > 0 {
		fmt.Println()
		for _, fr := range changed {
			fmt.Printf("  %-*s  %s   %s\n", nameW, fr.Name,
				st.green(fmt.Sprintf("+%d", fr.New)),
				st.dim(fmt.Sprintf("(%d total)", fr.Total)))
		}
	}
	// Hand-curated feeds are never fetched, so say plainly that they only
	// move when the user updates them.
	if len(manual) > 0 {
		fmt.Println()
		fmt.Printf("  %s update by hand (no feed to poll):\n",
			st.yellow("⚠"))
		sort.Slice(manual, func(i, j int) bool {
			return manual[i].Name < manual[j].Name
		})
		for _, fr := range manual {
			fmt.Printf("    %-*s  %s\n", nameW, fr.Name,
				st.dim(fmt.Sprintf("(%d total)", fr.Total)))
		}
	}
	if len(errored) > 0 {
		fmt.Println()
		for _, fr := range errored {
			fmt.Printf("  %s %s: %v\n", st.red("✗"), fr.Name, fr.Err)
		}
	}

	fmt.Println()
	errPart := fmt.Sprintf("%d errors", len(errored))
	if len(errored) > 0 {
		errPart = st.red(errPart)
	}
	// "new" counts articles but the rest count feeds, so say so — side by
	// side they otherwise read as the same unit.
	line := fmt.Sprintf("  %d new · %s unchanged",
		totNew, plural(unchanged, "feed"))
	if len(manual) > 0 {
		line += fmt.Sprintf(" · %s manual", plural(len(manual), "feed"))
	}
	fmt.Printf("%s · %s\n", line, errPart)
}

// fmtDur renders an elapsed duration compactly, e.g. "6.2s".
func fmtDur(d time.Duration) string {
	return d.Round(100 * time.Millisecond).String()
}

func init() {
	syncCmd.Flags().StringVar(&syncFeedFilter, "feed", "",
		"sync only this feed name")
	syncCmd.Flags().BoolVar(&syncForce, "force", false,
		"ignore cache and refetch even if not modified")
	syncCmd.Flags().IntVarP(&syncJobs, "jobs", "j", 0,
		"number of feeds to fetch in parallel (0 = default)")
	rootCmd.AddCommand(syncCmd)
}
