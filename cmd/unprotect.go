package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
)

var unprotectCmd = &cobra.Command{
	Use:   "unprotect",
	Short: "Remove a path from protected_paths",
	Long: `Remove a path from ~/.tidymymac/config.yaml's protected_paths list.

Example:
$ tidymymac unprotect --path ~/Documents/Work
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, _ := cmd.Flags().GetString("path")
		if p == "" {
			return fmt.Errorf("--path is required")
		}
		removed, err := config.RemoveProtectedPath(p)
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintln(cmd.OutOrStdout(), styles.Dim.Render("  not protected, nothing to remove: ")+p)
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  unprotected: ")+p)
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(unprotectCmd)
	unprotectCmd.Flags().String("path", "", "path to remove from protected_paths (required)")
}
