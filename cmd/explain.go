package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// explainCmd represents the explain command
var explainCmd = &cobra.Command{
	Use:   "explain",
	Short: "provides a detailed explanation of the possible junk files and their storage impact",
	Long: `Provides a detailed explanation of the possible junk files and their storage impact. 
This command helps users understand what types of files are considered junk and how they affect storage usage. It is useful for making informed decisions about which files to delete.

Example usage:
# Get an explanation of system data usage
$ tidymymac explain system-data 

# Get an explanation of docker storage usage
$ tidymymac explain docker
`,

	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("explain called")
	},
}

func init() {
	rootCmd.AddCommand(explainCmd)

}
