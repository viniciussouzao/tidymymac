package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
)

var protectCmd = &cobra.Command{
	Use:   "protect",
	Short: "Add a path to protected_paths so no cleaner can ever delete it",
	Long: `Add a path to ~/.tidymymac/config.yaml's protected_paths list.
Protected paths are a hard block: no cleaner, category, or --execute run can
ever delete anything under a protected path, regardless of CLI flags.

Example:
# Protect a directory
$ tidymymac protect --path ~/Documents/Work
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, _ := cmd.Flags().GetString("path")
		if p == "" {
			return fmt.Errorf("--path is required")
		}
		if err := config.AddProtectedPath(p); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  protected: ")+p)
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(protectCmd)
	protectCmd.Flags().String("path", "", "path to add to protected_paths (required)")
}
