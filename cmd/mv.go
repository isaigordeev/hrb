package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/config"
	"github.com/isdg/hr/internal/vault"
)

var (
	mvDryRun   bool
	mvNoConfig bool
)

var mvCmd = &cobra.Command{
	Use:   "mv <feed>... <group>",
	Short: "Move feed folders into a group under feeds/",
	Long: `Moves one or more feed directories into a folder under feeds/, so a
large vault can be shelved by kind:

  hr mv matklad danluu regehr humans
  hr mv lobsters lwn sites
  hr mv knuth turing books/classics
  hr mv lobsters /            # back to the top level

The group is a path relative to feeds/ and is created if missing; "/"
(or ".") means the feeds root. Group folders left empty by a move are
removed.

Feeds are named by their directory, so a feed hr has never fetched moves
just like a synced one. A shell-completed path works too: ` +
		"`hr mv feeds/matklad humans`." + `

Only directories move. Article files, sidecars, ids and read state are
untouched, and .hr/raw/ is keyed by feed name so it needs no change.
This is a plain rename — git records it as one on the next commit.

Declared feeds also get their ` + "`group`" + ` key updated in hr.toml (comments
and formatting are preserved); pass --no-config to skip that. The key
only decides where a feed with no directory yet lands on its first sync,
since the directory on disk is what actually determines a feed's group.`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, _, err := openActiveVault()
		if err != nil {
			return err
		}
		names, group := args[:len(args)-1], args[len(args)-1]

		moves, err := v.PlanMoves(names, group)
		if err != nil {
			return err
		}
		return runMoves(v, moves)
	},
}

func runMoves(v *vault.Vault, moves []vault.Move) error {
	groups := map[string]string{}
	moved := 0
	for _, m := range moves {
		if m.NoOp() {
			fmt.Printf("  = %-20s already in %s\n",
				m.Name, groupLabel(v.GroupOf(m.To)))
			continue
		}
		if mvDryRun {
			fmt.Printf("  → %-20s %s → %s\n",
				m.Name, v.Rel(m.From), v.Rel(m.To))
		} else {
			if err := v.ApplyMove(m); err != nil {
				return err
			}
			fmt.Printf("  ✓ %-20s %s\n", m.Name, v.Rel(m.To))
		}
		groups[m.Name] = v.GroupOf(m.To)
		moved++
	}

	if mvDryRun {
		fmt.Printf("\n%s (dry run, nothing changed)\n", plural(moved, "move"))
		return nil
	}
	if moved == 0 {
		fmt.Println("\nnothing to do")
		return nil
	}

	msg := plural(moved, "feed") + " moved"
	if !mvNoConfig {
		n, err := config.SetGroups(v.ConfigPath(), groups)
		if err != nil {
			// The directories already moved and are authoritative, so a
			// config write failure is a warning, not a failed command.
			fmt.Printf("\n%s (hr.toml not updated: %v)\n", msg, err)
			return nil
		}
		if n > 0 {
			msg += fmt.Sprintf(", %s updated in hr.toml", plural(n, "feed"))
		}
	}
	fmt.Printf("\n%s\n", msg)
	return nil
}

func groupLabel(group string) string {
	if group == "" {
		return "feeds/ (top level)"
	}
	return "feeds/" + group
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func init() {
	mvCmd.Flags().BoolVar(&mvDryRun, "dry-run", false,
		"show what would move without touching anything")
	mvCmd.Flags().BoolVar(&mvNoConfig, "no-config", false,
		"don't update the group key in hr.toml")
	rootCmd.AddCommand(mvCmd)
}
