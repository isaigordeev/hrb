package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/listing"
)

var (
	listUnread    bool
	listFeed      string
	listGroups    []string
	listTag       string
	listSince     string
	listCorrupted bool
	feedGroups    []string
	listTSV       bool
	listJSON      bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List articles, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, _, err := openActiveVault()
		if err != nil {
			return err
		}
		f, err := buildListFilter()
		if err != nil {
			return err
		}
		items, err := listing.List(v, f)
		if err != nil {
			return err
		}
		switch {
		case listJSON:
			return printJSON(items)
		case listTSV:
			return printTSV(items)
		default:
			return printPretty(items)
		}
	},
}

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "TSV of unread items, newest first (nvim-friendly)",
	RunE: func(cmd *cobra.Command, args []string) error {
		v, _, err := openActiveVault()
		if err != nil {
			return err
		}
		items, err := listing.List(v, listing.Filter{
			Unread: true,
			Groups: feedGroups,
		})
		if err != nil {
			return err
		}
		return printTSV(items)
	},
}

func buildListFilter() (listing.Filter, error) {
	f := listing.Filter{
		Feed:      listFeed,
		Groups:    listGroups,
		Tag:       listTag,
		Unread:    listUnread,
		Corrupted: listCorrupted,
	}
	if listSince != "" {
		d, err := listing.ParseSince(listSince)
		if err != nil {
			return f, fmt.Errorf("invalid --since: %w", err)
		}
		f.Since = d
	}
	return f, nil
}

func printTSV(items []listing.Item) error {
	for _, it := range items {
		// Group is appended, not inserted: existing consumers index
		// fields 1-6 and must keep working.
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			it.Path,
			it.Feed,
			it.Published.Format("2006-01-02"),
			boolBit(it.Read),
			boolBit(it.Favorite),
			tabSafe(it.Label()),
			it.Group,
		)
	}
	return nil
}

func boolBit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func tabSafe(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func printJSON(items []listing.Item) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

func printPretty(items []listing.Item) error {
	if len(items) == 0 {
		fmt.Println("(no articles)")
		return nil
	}
	feedW := 4
	for _, it := range items {
		if l := len(it.Feed); l > feedW {
			feedW = l
		}
	}
	if feedW > 20 {
		feedW = 20
	}
	for _, it := range items {
		feed := it.Feed
		if len(feed) > feedW {
			feed = feed[:feedW]
		}
		fmt.Printf("[%s%s%s] %s  %-*s  %s\n",
			stateChar(it.Read, "R"),
			stateChar(it.Favorite, "F"),
			stateChar(it.Corrupt > 0, "C"),
			it.Published.Format("2006-01-02"),
			feedW, feed,
			it.Label(),
		)
	}
	return nil
}

func stateChar(set bool, ch string) string {
	if set {
		return ch
	}
	return " "
}

func init() {
	listCmd.Flags().BoolVar(&listUnread, "unread", false,
		"only unread items")
	listCmd.Flags().StringVar(&listFeed, "feed", "",
		"filter to a single feed")
	listCmd.Flags().StringSliceVar(&listGroups, "group", nil,
		"filter to a folder under feeds/ (repeatable; subtree match; \"/\" = top level only)")
	listCmd.Flags().StringVar(&listTag, "tag", "",
		"filter by tag")
	listCmd.Flags().StringVar(&listSince, "since", "",
		"only items newer than (e.g. 7d, 24h)")
	listCmd.Flags().BoolVar(&listCorrupted, "corrupted", false,
		"only items with corruption marks")
	listCmd.Flags().BoolVar(&listTSV, "tsv", false,
		"tab-separated output (machine-friendly)")
	listCmd.Flags().BoolVar(&listJSON, "json", false,
		"JSON output")
	feedCmd.Flags().StringSliceVar(&feedGroups, "group", nil,
		"filter to a folder under feeds/ (repeatable; subtree match)")
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(feedCmd)
}
