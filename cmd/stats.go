package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// statsCmd represents the stats command
var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show all-time statistics of cleanup runs",
	Long: `Show all-time cleanup statistics.

Without arguments, this command shows the aggregate statistics for every
successful cleanup run recorded by TidyMyMac.

With one category argument, it shows the aggregate statistics for that
specific category across all recorded runs.

Example:

# Show all-time statistics for all categories
tidymymac stats

# Show all-time statistics for the "caches" category
tidymymac stats caches
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		record, err := history.Load()
		if err != nil {
			return fmt.Errorf("error loading history: %w. You can reset it by deleting the file at ~/.tidymymac/history.json", err)
		}

		if len(args) == 0 {
			fmt.Fprint(cmd.OutOrStdout(), renderAllTimeStats(history.Stats(record), time.Local))
			return nil
		}

		categoryName := args[0]
		if err := validateHistoryCategory(categoryName); err != nil {
			return err
		}

		category := cleaner.Category(categoryName)
		fmt.Fprint(cmd.OutOrStdout(), renderCategoryStats(category.DisplayName(), history.StatsByCategory(record, categoryName), time.Local))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

func validateHistoryCategory(name string) error {
	registry := cleaner.DefaultRegistry()
	if _, exists := registry.Get(cleaner.Category(name)); exists {
		return nil
	}

	available := make([]string, 0, len(registry.All()))
	for _, cat := range registry.All() {
		available = append(available, string(cat.Category()))
	}

	slices.Sort(available)

	var b strings.Builder

	b.WriteString(fmt.Sprintf("unknown category %q. Available categories:\n", name))
	for _, category := range available {
		b.WriteString("  ")
		b.WriteString(category)
		b.WriteString("\n")
	}

	return fmt.Errorf("%s", strings.TrimSuffix(b.String(), "\n"))
}

func renderAllTimeStats(stats history.AllTimeStats, locTime *time.Location) string {
	return renderStatsBlock(
		"  all-time stats",
		stats,
		locTime,
	)
}

func renderCategoryStats(displayName string, stats history.AllTimeStats, locTime *time.Location) string {
	return renderStatsBlock(
		fmt.Sprintf("  stats for %s", displayName),
		stats,
		locTime,
	)
}

func renderStatsBlock(title string, stats history.AllTimeStats, locTime *time.Location) string {
	if stats.TotalRuns == 0 {
		return scanHelpStyle.Render(title) + scanHelpStyle.Render("  no recorded runs yet.") + "\n"
	}

	boldStyle := lipgloss.NewStyle().Bold(true)
	const statsTableWidth = 40
	sep := scanDimStyle.Render("  " + strings.Repeat("─", statsTableWidth))

	var b strings.Builder

	b.WriteString(boldStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %-14s %s\n", "Total runs:", scanDimStyle.Render(fmt.Sprintf("%d", stats.TotalRuns))))
	b.WriteString(fmt.Sprintf("  %-14s %s\n", "Total files:", scanDimStyle.Render(fmt.Sprintf("%d", stats.TotalFiles))))
	b.WriteString(fmt.Sprintf("  %-14s %s\n", "Total bytes:", scanDimStyle.Render(utils.FormatBytes(stats.TotalBytes))))
	b.WriteString(fmt.Sprintf("  %-14s %s\n", "Avg per run:", scanDimStyle.Render(utils.FormatBytes(stats.AvgBytes))))
	b.WriteString(fmt.Sprintf("  %-14s %s\n", "Last run at:", scanDimStyle.Render(stats.LastRunAt.In(locTime).Format("2006-01-02 15:04:05"))))
	b.WriteString("\n")
	return b.String()

}
