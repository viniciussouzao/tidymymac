package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
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

# Scan the docker storage and provide a summary of the findings
$ tidymymac scan docker
`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("scan called")
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)

}
