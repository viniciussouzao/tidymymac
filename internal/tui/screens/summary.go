package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// SummaryModel is the final screen showing cleanup results.
type SummaryModel struct {
	Results    []*cleaner.CleanResult
	TotalFreed int64
	TotalFiles int
	TotalTime  time.Duration
	ErrorCount int
	DryRun     bool
	Width      int
	Height     int
}

// NewSummary creates a summary from clean results.
func NewSummary(results []*cleaner.CleanResult, dryRun bool) SummaryModel {
	m := SummaryModel{
		Results: results,
		DryRun:  dryRun,
	}

	for _, r := range results {
		if r.Skipped {
			continue
		}
		m.TotalFreed += r.BytesFreed
		m.TotalFiles += r.FilesDeleted
		m.TotalTime += r.Duration
		m.ErrorCount += len(r.Errors)
	}

	return m
}

// SetSize updates dimensions.
func (m *SummaryModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

// View renders the summary screen.
func (m SummaryModel) View() string {
	var b strings.Builder

	if m.DryRun {
		b.WriteString(styles.SuccessTitle.Render("Dry Run Complete!"))
	} else {
		b.WriteString(styles.SuccessTitle.Render("Cleanup Complete!"))
	}
	b.WriteString("\n\n")

	// Table header.
	header := fmt.Sprintf("  %-22s %12s %10s", "Category", "Space", "Files")
	b.WriteString(styles.Dim.Render(header))
	b.WriteString("\n")
	b.WriteString(styles.Dim.Render("  " + strings.Repeat("─", 46)))
	b.WriteString("\n")

	for _, r := range m.Results {
		if r.Skipped {
			line := fmt.Sprintf("  %-22s %12s %10s",
				r.Category.DisplayName(),
				"—",
				"—",
			)
			b.WriteString(styles.Dim.Render(line))
			b.WriteString(styles.Warning.Render(" (skipped: requires sudo)"))
			b.WriteString("\n")
			continue
		}
		line := fmt.Sprintf("  %-22s %12s %10d",
			r.Category.DisplayName(),
			utils.FormatBytes(r.BytesFreed),
			r.FilesDeleted,
		)
		b.WriteString(styles.Plain.Render(line))

		if len(r.Errors) > 0 {
			b.WriteString(styles.Error.Render(fmt.Sprintf(" (%d errors)", len(r.Errors))))
		}
		b.WriteString("\n")
	}

	b.WriteString(styles.Dim.Render("  " + strings.Repeat("─", 46)))
	b.WriteString("\n")

	totalLine := fmt.Sprintf("  %-22s %12s %10d", "Total", utils.FormatBytes(m.TotalFreed), m.TotalFiles)
	b.WriteString(styles.Success.Render(totalLine))
	b.WriteString("\n\n")

	b.WriteString(styles.Dim.Render(fmt.Sprintf("  Time elapsed: %s", m.TotalTime.Round(time.Millisecond))))
	b.WriteString("\n")

	if m.ErrorCount > 0 {
		b.WriteString(styles.Warning.Render(fmt.Sprintf("  %d errors occurred (permission denied, etc.)", m.ErrorCount)))
		b.WriteString("\n")
	}

	if m.DryRun {
		b.WriteString("\n")
		b.WriteString(styles.Warning.Render("  Run with --execute to actually delete these files."))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styles.Help.Render("  Press enter to re-run or q to quit"))

	return b.String()
}
