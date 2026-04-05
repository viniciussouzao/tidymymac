package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/explain"
)

var explainCmd = &cobra.Command{
	Use:   "explain <profile>",
	Short: "Explain a macOS storage category using supported TidyMyMac contributors",
	Long: `Explain a macOS storage category using supported TidyMyMac contributors.

Example usage:
# Explain System Data usage
$ tidymymac explain system-data
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile, err := explain.ResolveProfile(explain.Profile(args[0]), cleaner.DefaultRegistry())
		if err != nil {
			return err
		}

		result, err := explain.RunProfile(cmd.Context(), profile)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprint(cmd.OutOrStdout(), explain.FormatProfileResult(result)); err != nil {
			return err
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(explainCmd)
}
