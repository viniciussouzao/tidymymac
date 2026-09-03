package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
		if executeFlag {
			if err := guardRootDeletion(); err != nil {
				return err
			}
		}

		if warning := rootExecuteDeprecationWarning(cmd); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}
		p := tea.NewProgram(tui.NewApp(cmd.Context(), executeFlag, loadedConfig), tea.WithAltScreen())
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

// Execute runs the root command with a signal-aware context.
//
// Every RunE therefore reaches a cancellable ctx through cmd.Context(), which
// matters most for the hidden elevated helper: it runs as root and deletes
// files, and without a cancellable context its cleaners' ctx.Done() checks
// could never fire. sudo relays SIGTERM to the command it runs, so the
// unprivileged parent canceling its Invoke actually stops the root child
// here rather than orphaning a process that keeps deleting.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := rootCmd.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&executeFlag, "execute", "e", false, "execute deletions when cleaning ('clean --execute'); deprecated for the root TUI - use 'tidymymac execute' instead")
}
