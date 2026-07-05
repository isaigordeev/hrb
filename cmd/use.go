package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/rc"
	"github.com/isdg/hr/internal/vault"
)

var useClear bool

var useCmd = &cobra.Command{
	Use:   "use [dir]",
	Short: "Set (or show) the global default vault",
	Long: `Sets the vault hr falls back to when run outside any vault: no -C
flag, no $HR_VAULT, and the current directory isn't inside a vault
(walking up for hr.toml, like a cloned vault you've cd'd into).

  hr use <dir>      point the global default at an existing vault
  hr use            print the current global default
  hr use --clear    unset it

This only writes ~/.hrrc. It never creates a vault: run 'hr init' for
that, or clone one and cd into it directly -- no ~/.hrrc needed there.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if useClear {
			if len(args) > 0 {
				return fmt.Errorf("--clear takes no path")
			}
			if err := rc.Save(&rc.RC{}); err != nil {
				return err
			}
			fmt.Println("cleared global default vault")
			return nil
		}
		if len(args) == 0 {
			r, err := rc.Load()
			if err != nil {
				return err
			}
			if r.Vault == "" {
				fmt.Println("(no global default vault set)")
				return nil
			}
			fmt.Println(r.Vault)
			return nil
		}

		root, err := vault.Resolve(args[0])
		if err != nil {
			return err
		}
		if _, err := vault.Open(root); err != nil {
			return err
		}
		if err := rc.Save(&rc.RC{Vault: root}); err != nil {
			return err
		}
		fmt.Printf("global default vault set to %s\n", root)
		return nil
	},
}

func init() {
	useCmd.Flags().BoolVar(&useClear, "clear", false,
		"unset the global default vault")
	rootCmd.AddCommand(useCmd)
}
