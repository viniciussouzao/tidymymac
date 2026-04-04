package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available categories",
	Long: `List can return different types of information depending on the subcommand used. For example:

# List all available categories
tidymymac list categories		
`,
}

var listCategoriesCmd = &cobra.Command{
	Use:   "categories",
	Short: "List available categories",
	Long: `List all available categories that TidyMyMac can clean or scan. 
This is useful to know which categories you can target when running tidymymac scan or tidymymac clean with a specific category argument.

Example:

# List all available categories
tidymymac list categories
`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(cmd.OutOrStdout(), returnCategories())
	},
}

func returnCategories() string {
	var b strings.Builder
	sep := scanDimStyle.Render("  " + strings.Repeat("─", 40))

	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")

	categories := cleaner.DefaultRegistry()
	for _, c := range categories.All() {
		b.WriteString("  " + string(c.Category()) + "\n")
	}

	b.WriteString(scanHelpStyle.Render("  run tidymymac scan/clean <category> to perform a scan or cleanup for a specific category"))
	b.WriteString("\n")

	return b.String()

}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.AddCommand(listCategoriesCmd)

}
