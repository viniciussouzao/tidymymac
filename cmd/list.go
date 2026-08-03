package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
)

type listOptions struct {
	detailed bool
}

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available resources (e.g. categories)",
	Long: `List available resources used by TidyMyMac.

Use a subcommand to specify what to list:

# List all available categories
tidymymac list categories
`,
}

var listCategoriesCmd = &cobra.Command{
	Use:   "categories",
	Short: "List all categories that can be scanned or cleaned",
	Long: `List all available categories that TidyMyMac can clean or scan.
This is useful to know which categories you can target when running tidymymac scan or tidymymac clean with a specific category argument.

Example:

# List all available categories
tidymymac list categories
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		detailed, _ := cmd.Flags().GetBool("detailed")
		if _, err := fmt.Fprint(cmd.OutOrStdout(), returnCategories(listOptions{detailed: detailed})); err != nil {
			return fmt.Errorf("write categories output: %w", err)
		}
		return nil
	},
}

func returnCategories(opts listOptions) string {
	var b strings.Builder
	sep := styles.Dim.Render("  " + strings.Repeat("─", 40))

	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")

	categories := cleaner.DefaultRegistry()
	for _, c := range categories.All() {
		line := "  " + string(c.Category())
		if loadedConfig.IsCategoryDisabled(string(c.Category())) {
			line += styles.Dim.Render(" (disabled by config)")
		}
		b.WriteString(line + "\n")
		if opts.detailed {
			b.WriteString("    " + styles.Dim.Render(c.Description()) + "\n")
		}
	}

	b.WriteString(styles.Help.Render("  run tidymymac scan/clean <category> to perform a scan or cleanup for a specific category"))
	b.WriteString("\n")

	return b.String()

}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.AddCommand(listCategoriesCmd)
	listCategoriesCmd.Flags().Bool("detailed", false, "show a description for each category")
}
