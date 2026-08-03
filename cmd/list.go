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

var listProtectedCmd = &cobra.Command{
	Use:   "protected",
	Short: "List protected paths and disabled categories from the config file",
	Long: `List the current safety configuration loaded from
~/.tidymymac/config.yaml: paths that are hard-blocked from deletion, and
categories disabled by default.

Example:
$ tidymymac list protected
`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(cmd.OutOrStdout(), returnProtected())
	},
}

func returnProtected() string {
	var b strings.Builder
	sep := styles.Dim.Render("  " + strings.Repeat("─", 40))

	b.WriteString("\n")
	b.WriteString(styles.CategoryHeader.Render("  Protected paths"))
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")
	if len(loadedConfig.ProtectedPaths) == 0 {
		b.WriteString(styles.Dim.Render("  (none configured)") + "\n")
	}
	for _, p := range loadedConfig.ProtectedPaths {
		b.WriteString("  " + p + "\n")
	}

	b.WriteString("\n")
	b.WriteString(styles.CategoryHeader.Render("  Disabled categories"))
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")
	if len(loadedConfig.DisabledCategories) == 0 {
		b.WriteString(styles.Dim.Render("  (none configured)") + "\n")
	}
	for _, c := range loadedConfig.DisabledCategories {
		b.WriteString("  " + c + "\n")
	}

	b.WriteString(styles.Help.Render("  run tidymymac protect --path <path> to add a protected path"))
	b.WriteString("\n")

	return b.String()
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.AddCommand(listCategoriesCmd)
	listCmd.AddCommand(listProtectedCmd)
	listCategoriesCmd.Flags().Bool("detailed", false, "show a description for each category")
}
