package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"

	"github.com/isdg/hr/internal/article"
	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/errlog"
	"github.com/isdg/hr/internal/meta"
	"github.com/isdg/hr/internal/vault"
)

var importNomCmd = &cobra.Command{
	Use:   "import-nom [dbpath]",
	Short: "Import historical articles from a nom SQLite database",
	Long: "Pulls every item from nom's SQLite db into the active hr " +
		"vault. Existing articles are left untouched; missing " +
		"articles are written along with their sidecar (read state, " +
		"favorite). Raw HTML is backfilled into .hr/raw/ for both new " +
		"and existing articles. Items whose feedurl is not in your " +
		"hr.toml are skipped (summary at end).",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dbpath := defaultNomDBPath()
		if len(args) == 1 {
			dbpath = args[0]
		}
		v, cfg, err := openActiveVault()
		if err != nil {
			return err
		}
		return runNomImport(dbpath, v, cfg)
	},
}

func defaultNomDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(
		home, "Library/Application Support/nom/nom.db")
}

func runNomImport(
	dbpath string, v *vault.Vault, cfg *config.Config,
) error {
	db, err := sql.Open("sqlite", dbpath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	feedByURL := make(map[string]config.Feed, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		// Manual feeds carry no url: they would all collide on the empty
		// key, and no nom row can match them anyway.
		if f.URL == "" {
			continue
		}
		feedByURL[f.URL] = f
	}

	// Resolve each feed's directory once: they may be grouped under
	// feeds/ at any depth, and this import creates thousands of files.
	loc, err := v.Locate()
	if err != nil {
		return fmt.Errorf("locate feed directories: %w", err)
	}
	feedDirs := make(map[string]string, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		dir, err := loc.Dir(f.Name, f.Group)
		if err != nil {
			return err
		}
		feedDirs[f.Name] = dir
	}

	rows, err := db.Query(`
		SELECT feedurl, link, title, content, readat,
		       publishedat, favourite, guid
		FROM items`)
	if err != nil {
		return fmt.Errorf("query items: %w", err)
	}
	defer rows.Close()

	conv := md.NewConverter("", true, nil)
	elog := errlog.New(filepath.Join(v.MetaDir(), "err.txt"))

	var (
		imported, alreadyHave, rawWritten, missing, total int
		missingFeeds                                      = map[string]int{}
	)

	for rows.Next() {
		total++
		var (
			feedurl, link, title, content, guid string
			readat, publishedat                 sql.NullTime
			favourite                           bool
		)
		err := rows.Scan(
			&feedurl, &link, &title, &content,
			&readat, &publishedat, &favourite, &guid)
		if err != nil {
			elog.Write("import-nom:scan", err)
			continue
		}

		cf, ok := feedByURL[feedurl]
		if !ok {
			missing++
			missingFeeds[feedurl]++
			continue
		}
		feedName := cf.Name

		body, err := conv.ConvertString(content)
		if err != nil {
			body = content
		}

		pub := time.Now().UTC()
		if publishedat.Valid {
			pub = publishedat.Time
		}

		a := &article.Article{
			Title:     title,
			URL:       link,
			Published: pub,
			FeedName:  feedName,
			GUID:      guid,
			Body:      body,
		}

		written, path, err := article.Write(feedDirs[feedName], a)
		if err != nil {
			elog.Write("import-nom:write", err)
			continue
		}
		if written {
			imported++
			m := &meta.Meta{Favorite: favourite}
			if readat.Valid {
				m.Read = true
				t := readat.Time
				m.ReadAt = &t
			}
			if err := meta.Save(path, m); err != nil {
				elog.Write("import-nom:meta", err)
			}
		} else {
			alreadyHave++
		}

		rawPath := v.RawPath(feedName, a.Filename())
		if _, err := os.Stat(rawPath); errors.Is(err, os.ErrNotExist) {
			err := os.MkdirAll(filepath.Dir(rawPath), 0o755)
			if err == nil {
				if e := os.WriteFile(
					rawPath, []byte(content), 0o644,
				); e == nil {
					rawWritten++
				}
			}
		}

		if total%500 == 0 {
			fmt.Printf("processed %d items...\n", total)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	fmt.Println()
	fmt.Printf("imported new articles : %d\n", imported)
	fmt.Printf("already in vault      : %d\n", alreadyHave)
	fmt.Printf("raw HTML written      : %d\n", rawWritten)
	fmt.Printf("skipped (unknown feed): %d items across %d URLs\n",
		missing, len(missingFeeds))
	if len(missingFeeds) > 0 {
		fmt.Println()
		fmt.Println("Feed URLs in nom but not in hr.toml:")
		for url, count := range missingFeeds {
			fmt.Printf("  %4d  %s\n", count, url)
		}
	}
	return nil
}

func init() { rootCmd.AddCommand(importNomCmd) }
