package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui"
)

var executeFlag bool
var loadedConfig *config.Config

var rootCmd = &cobra.Command{
	Use:   "tidymymac",
	Short: "macOS storage cleanup tool",
	Long: `TidyMyMac scans for junk files and helps you clean up your Mac storage.

Running without a subcommand opens the interactive TUI where you can browse
and select categories to clean, in dry-run mode by default. Use
'tidymymac execute' to open the same TUI ready to delete, or use
subcommands for non-interactive workflows.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		loadedConfig = cfg
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if warning := rootExecuteDeprecationWarning(cmd); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}
		p := tea.NewProgram(tui.NewApp(executeFlag, loadedConfig), tea.WithAltScreen())
		_, err := p.Run()
		return err
	},
}

// rootExecuteDeprecationWarning returns a one-line deprecation warning when
// --execute was explicitly passed to invoke the root TUI directly, or ""
// otherwise. It does not affect 'clean --execute', which has its own RunE
// and never calls this function.
func rootExecuteDeprecationWarning(cmd *cobra.Command) string {
	if !cmd.Flags().Changed("execute") {
		return ""
	}
	return "⚠️  --execute on the root command is deprecated; use 'tidymymac execute' instead."
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&executeFlag, "execute", "e", false, "execute deletions when cleaning ('clean --execute'); deprecated for the root TUI - use 'tidymymac execute' instead")
}
