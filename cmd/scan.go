package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// scanCmd represents the scan command
var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scans the system for junk files and other unnecessary data without entering the TUI",
	Long: `Scans the system for junk files and other unnecessary data. 
This command helps users identify files that can be safely removed to free up disk space.

Example usage:
# Scan the system for junk files
$ tidymymac scan

# Scan the system data and provide a summary of the findings
$ tidymymac scan system-data

# Scan multiple categories at once (e.g., caches and Docker-related files)
$ tidymymac scan docker caches
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := commands.RunScan(cmd.Context(), cleaner.DefaultRegistry(), args)
		if err != nil {
			return err
		}

		for _, cat := range result.Categories {
			if cat.Err != nil {
				fmt.Fprintf(cmd.OutOrStderr(), "%s: error scanning category: %v\n", cat.Name, cat.Err)
				continue
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%-24s %8d files %12s\n", cat.Name, cat.TotalFiles, utils.FormatBytes(cat.TotalSize))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "\n%-24s %8d files %12s\n",
			"Total",
			result.TotalFiles,
			utils.FormatBytes(result.TotalSize),
		)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)

}
