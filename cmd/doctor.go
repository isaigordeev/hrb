package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/isdg/hr/internal/doctor"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the vault for filesystem/config inconsistencies",
	Long: `Checks hr.toml and every feed directory for problems hr's normal
commands don't otherwise surface: malformed sidecars, orphaned files,
tombstone contradictions, id collisions, and config/directory drift.

Read-only: doctor never modifies the vault. Exits non-zero if any
error-level issue was found.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, cfg, err := openActiveVault()
		if err != nil {
			return err
		}
		report, err := doctor.Check(v, cfg)
		if err != nil {
			return err
		}
		if doctorJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report.Issues); err != nil {
				return err
			}
		} else {
			printDoctorReport(report)
		}
		if report.HasErrors() {
			os.Exit(1)
		}
		return nil
	},
}

func printDoctorReport(r doctor.Report) {
	if len(r.Issues) == 0 {
		fmt.Println("vault looks healthy")
		return
	}
	for _, i := range r.Issues {
		path := i.Path
		if path == "" {
			path = "-"
		}
		fmt.Printf("%-5s %-40s %s\n", i.Severity, path, i.Message)
	}
	fmt.Printf("\n%d error(s), %d warning(s), %d info\n",
		r.Count(doctor.Error), r.Count(doctor.Warn), r.Count(doctor.Info))
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "JSON output")
	rootCmd.AddCommand(doctorCmd)
}
