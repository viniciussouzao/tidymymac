package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// historyCmd represents the history command
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Displays the history of actions performed by the application",
	Long: `Displays the history of actions performed by the application.
This command helps users review past actions and understand the sequence of events.

Example usage:
# View the history of actions
$ tidymymac history

# View the stats of the execution history
$ tidymymac history --stats
`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("history called")
	},
}

func init() {
	rootCmd.AddCommand(historyCmd)
}
