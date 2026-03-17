package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
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

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#10B981")).MarginBottom(1)

	catStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA"))

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).MarginTop(1)

	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))

	if m.DryRun {
		b.WriteString(titleStyle.Render("Dry Run Complete!"))
	} else {
		b.WriteString(titleStyle.Render("Cleanup Complete!"))
	}
	b.WriteString("\n\n")

	// Table header.
	header := fmt.Sprintf("  %-22s %12s %10s", "Category", "Space", "Files")
	b.WriteString(dimStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 46)))
	b.WriteString("\n")

	for _, r := range m.Results {
		line := fmt.Sprintf("  %-22s %12s %10d",
			string(r.Category),
			utils.FormatBytes(r.BytesFreed),
			r.FilesDeleted,
		)
		b.WriteString(catStyle.Render(line))

		if len(r.Errors) > 0 {
			b.WriteString(errorStyle.Render(fmt.Sprintf(" (%d errors)", len(r.Errors))))
		}
		b.WriteString("\n")
	}

	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 46)))
	b.WriteString("\n")

	totalLine := fmt.Sprintf("  %-22s %12s %10d", "Total", utils.FormatBytes(m.TotalFreed), m.TotalFiles)
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(totalLine))
	b.WriteString("\n\n")

	b.WriteString(dimStyle.Render(fmt.Sprintf("  Time elapsed: %s", m.TotalTime.Round(time.Millisecond))))
	b.WriteString("\n")

	if m.ErrorCount > 0 {
		b.WriteString(warnStyle.Render(fmt.Sprintf("  %d errors occurred (permission denied, etc.)", m.ErrorCount)))
		b.WriteString("\n")
	}

	if m.DryRun {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("  Run with --execute to actually delete these files."))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  Press enter to re-run or q to quit"))

	return b.String()
}
