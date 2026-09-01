package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/tui"
)

var executeCmd = &cobra.Command{
	Use:   "execute",
	Short: "Open the interactive TUI ready to delete",
	Long: `Open the same interactive TUI as running tidymymac with no subcommand,
but already in execute mode. Scanned categories can still be reviewed -
including the count and size of what will be removed - and deletion still
requires the existing in-TUI confirmation before anything is deleted.

Example usage:
# Open the TUI in execute mode
$ tidymymac execute
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(tui.NewApp(true, loadedConfig), tea.WithAltScreen())
		_, err := p.Run()
		return err
	},
}

func init() {
	rootCmd.AddCommand(executeCmd)
}
