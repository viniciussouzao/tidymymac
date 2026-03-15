package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/tui"
)

var executeFlag bool

var rootCmd = &cobra.Command{
	Use:   "tidymymac",
	Short: "MacOS storage cleanup tool",
	Long:  `TidyMyMac scans for junk files and helps you clean up your Mac storage.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(tui.NewApp(executeFlag), tea.WithAltScreen())
		_, err := p.Run()
		return err
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&executeFlag, "execute", "e", false, "Actually delete files (default is dry-run)")
}
