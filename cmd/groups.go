package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/listing"
)

var groupsJSON bool

var groupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List the folders under feeds/ and what they hold",
	Long: `Shows every group in the vault with its feed, article and unread
counts. Counts cover each group's own feeds, not its subgroups, so they
sum to the vault total instead of double-counting nested shelves.

Use ` + "`hr list --group <name>`" + ` to read one shelf; that filter is by
subtree, so --group humans also covers humans/archive.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, _, err := openActiveVault()
		if err != nil {
			return err
		}
		groups, err := listing.Groups(v)
		if err != nil {
			return err
		}
		if groupsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(groups)
		}
		printGroups(groups)
		return nil
	},
}

func printGroups(groups []listing.Group) {
	if len(groups) == 0 {
		fmt.Println("(no feeds)")
		return
	}
	nameW := 5
	for _, g := range groups {
		if l := len(groupDisplayName(g.Name)); l > nameW {
			nameW = l
		}
	}
	var feeds, articles, unread int
	for _, g := range groups {
		fmt.Printf("%-*s  %4d feeds  %6d articles  %6d unread\n",
			nameW, groupDisplayName(g.Name), g.Feeds, g.Articles, g.Unread)
		feeds += g.Feeds
		articles += g.Articles
		unread += g.Unread
	}
	fmt.Printf("\n%-*s  %4d feeds  %6d articles  %6d unread\n",
		nameW, "total", feeds, articles, unread)
}

// groupDisplayName labels the feeds root, which has no name of its own.
func groupDisplayName(name string) string {
	if name == "" {
		return "(top level)"
	}
	return name
}

func init() {
	groupsCmd.Flags().BoolVar(&groupsJSON, "json", false, "JSON output")
	rootCmd.AddCommand(groupsCmd)
}
