package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

type historyOptions struct {
	last    int
	all     bool
	verbose bool
}

// historyCmd represents the history command
var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show recent successful cleanup runs",
	Long: `Show recent successful cleanup runs recorded by TidyMyMac.

By default, this command shows the last 5 successful execute-mode runs.
Use --all to display the full history or --verbose to expand each run by category.

Example:

# Show the last 5 successful runs
tidymymac history

# Show the full history of successful runs
tidymymac history --all

# Show the last 5 successful runs with category details
tidymymac history --verbose
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		last, _ := cmd.Flags().GetInt("last")
		all, _ := cmd.Flags().GetBool("all")
		verbose, _ := cmd.Flags().GetBool("verbose")

		if all && cmd.Flags().Changed("last") {
			return fmt.Errorf("--all and --last cannot be used together")
		}

		if !all && last <= 0 {
			return fmt.Errorf("--last must be greater than 0")
		}

		records, err := history.Load()
		if err != nil {
			return fmt.Errorf("error loading history: %w. You can reset it by deleting the file at ~/.tidymymac/history.json", err)
		}

		opts := historyOptions{
			last:    last,
			all:     all,
			verbose: verbose,
		}

		historyOutput, err := renderHistory(records, opts, time.Local)
		if err != nil {
			return fmt.Errorf("error rendering history: %w", err)
		}

		fmt.Fprint(cmd.OutOrStdout(), historyOutput)
		return nil

	},
}

func init() {
	rootCmd.AddCommand(historyCmd)
	historyCmd.Flags().BoolP("all", "a", false, "show the full history of successful runs")
	historyCmd.Flags().BoolP("verbose", "v", false, "show per-category breakdown for each run")
	historyCmd.Flags().Int("last", 5, "number of recent runs to show (ignored if --all is set)")
}

func renderHistory(record history.Record, opts historyOptions, loc *time.Location) (string, error) {
	var b strings.Builder
	boldStyle := lipgloss.NewStyle().Bold(true)

	const (
		historyColRun      = 4
		historyColRanAt    = 16
		historyColFreed    = 12
		historyColFiles    = 10
		historyColDuration = 8
		historyTreeIndent  = 7
		historyTreeFreed   = 12
		historyTreeFiles   = 10
		historyTreeNameMin = 16
	)
	historyTableWidth := historyColRun + historyColRanAt + historyColFreed + historyColFiles + historyColDuration + 8
	sep := scanDimStyle.Render("  " + strings.Repeat("─", historyTableWidth))

	if len(record.Runs) == 0 {
		b.WriteString(scanHelpStyle.Render("  no cleanup history found. Run tidymymac to get started."))
		b.WriteString("\n")
		return b.String(), nil
	}

	runs := slices.Clone(record.Runs)
	slices.SortFunc(runs, func(a, b history.RunRecord) int {
		if a.RanAt.After(b.RanAt) {
			return -1 // a is more recent than b
		}

		if a.RanAt.Before(b.RanAt) {
			return 1
		}

		if a.ID > b.ID {
			return -1
		}

		if a.ID < b.ID {
			return 1
		}

		return 0
	})

	if !opts.all && len(runs) > opts.last {
		runs = runs[:opts.last]
	}

	b.WriteString(fmt.Sprintf("\n  %s  %s  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", historyColRun, "ID")),
		boldStyle.Render(fmt.Sprintf("%-*s", historyColRanAt, "Ran At")),
		boldStyle.Render(fmt.Sprintf("%*s", historyColFreed, "Freed")),
		boldStyle.Render(fmt.Sprintf("%*s", historyColFiles, "Files")),
		boldStyle.Render(fmt.Sprintf("%*s", historyColDuration, "Duration")),
	))
	b.WriteString(sep)
	b.WriteString("\n")

	for i, run := range runs {
		// default info
		b.WriteString(fmt.Sprintf(
			"  %-*s  %-*s  %s  %s  %s\n",
			historyColRun, fmt.Sprintf("#%d", run.ID),
			historyColRanAt, run.RanAt.In(loc).Format("2006-01-02 15:04"),
			scanDimStyle.Render(fmt.Sprintf("%*s", historyColFreed, utils.FormatBytes(run.TotalBytes))),
			scanDimStyle.Render(fmt.Sprintf("%*d", historyColFiles, run.TotalFiles)),
			scanDimStyle.Render(fmt.Sprintf("%*s", historyColDuration, formatHistoryDuration(time.Duration(run.DurationMs)*time.Millisecond))),
		))

		// verbose
		if opts.verbose && len(run.Categories) > 0 {
			categoriesTree := pterm.TreeNode{}

			for _, cat := range run.Categories {
				categoriesTree.Children = append(categoriesTree.Children, pterm.TreeNode{
					Text: scanDimStyle.Render(cat.DisplayName),
					Children: []pterm.TreeNode{
						{Text: scanDimStyle.Render("Freed: " + utils.FormatBytes(cat.Bytes))},
						{Text: scanDimStyle.Render(fmt.Sprintf("Files: %d", cat.Files))},
					},
				})
			}

			treeText, err := pterm.DefaultTree.
				WithRoot(categoriesTree).
				WithIndent(2).
				Srender()
			if err != nil {
				return "failed to render category tree: " + err.Error(), err
			}

			b.WriteString(indentMultiline(treeText, "  "))
		}

		if i < len(runs)-1 {
			b.WriteString("\n")
		}
	}

	b.WriteString(sep)
	b.WriteString("\n")

	if !opts.all {
		b.WriteString(scanHelpStyle.Render(fmt.Sprintf("  Showing the last %d runs. Use --all to view the full history.", len(runs))))
		b.WriteString("\n")
	}

	return b.String(), nil
}

func formatHistoryDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	return d.Round(time.Second).String()
}

func indentMultiline(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}

	return strings.Join(lines, "\n")
}
