package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "delete the junk files without opening the TUI",
	Long:  `Deletes the junk files without opening the TUI. This is a non-interactive mode that can be used for automation or scripting purposes. Use with caution, as it will permanently delete files without confirmation.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("clean called")
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}
