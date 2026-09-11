package screens

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/celebration"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// SummaryModel is the final screen showing cleanup results.
type SummaryModel struct {
	Results     []*cleaner.CleanResult
	TotalFreed  int64
	TotalFiles  int
	TotalTime   time.Duration
	ErrorCount  int
	DryRun      bool
	Celebration string
	Width       int
	Height      int
	// ShowErrors expands every category's collapsed error summary into the
	// full per-item list (path: reason) already carried on CleanResult.Errors
	// -- toggled by the user, never computed here.
	ShowErrors bool

	// Breakdown carries, per category, how many entries were Protected or
	// excluded by the user on the review screen before this run's plan was
	// even built -- see ReviewBreakdown and App.reviewBreakdown. A category
	// with a zero-value (or absent) entry shows no suffix; the existing
	// Skipped/SkipReason rendering (the sudo-skip "not selected" case)
	// already covers the rest of the card's "separa itens limpos, excluídos
	// pelo usuário, protegidos e não selecionados" requirement on its own.
	Breakdown map[cleaner.Category]ReviewBreakdown
}

// NewSummary creates a summary from clean results.
func NewSummary(results []*cleaner.CleanResult, dryRun bool, breakdown map[cleaner.Category]ReviewBreakdown) SummaryModel {
	m := SummaryModel{
		Results:   results,
		DryRun:    dryRun,
		Breakdown: breakdown,
	}

	for _, r := range results {
		if r == nil {
			continue
		}
		if r.Skipped {
			continue
		}
		m.TotalFreed += r.BytesFreed
		m.TotalFiles += r.FilesDeleted
		m.TotalTime += r.Duration
		m.ErrorCount += len(r.Errors)
	}

	if !dryRun {
		m.Celebration = celebration.Message(celebrationResults(results))
	}

	return m
}

func celebrationResults(results []*cleaner.CleanResult) []celebration.Result {
	converted := make([]celebration.Result, 0, len(results))
	for _, result := range results {
		if result == nil || result.Skipped {
			continue
		}
		converted = append(converted, celebration.Result{
			Category:   result.Category,
			BytesFreed: result.BytesFreed,
			Failed:     len(result.Errors) > 0,
		})
	}
	return converted
}

// SetSize updates dimensions.
func (m *SummaryModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

// ToggleShowErrors expands or collapses each category's error detail list.
func (m *SummaryModel) ToggleShowErrors() {
	m.ShowErrors = !m.ShowErrors
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
			reason := r.SkipReason
			if reason == "" {
				reason = "skipped"
			}
			line := fmt.Sprintf("  %-22s %12s %10s",
				r.Category.DisplayName(),
				"—",
				"—",
			)
			b.WriteString(styles.Dim.Render(line))
			b.WriteString(styles.Warning.Render(" (" + reason + ")"))
			b.WriteString("\n")
			continue
		}
		line := fmt.Sprintf("  %-22s %12s %10d",
			r.Category.DisplayName(),
			utils.FormatBytes(r.BytesFreed),
			r.FilesDeleted,
		)
		b.WriteString(styles.Plain.Render(line))

		if bd, ok := m.Breakdown[r.Category]; ok && (bd.ExcludedByUser > 0 || bd.Protected > 0) {
			var parts []string
			if bd.ExcludedByUser > 0 {
				parts = append(parts, fmt.Sprintf("%d excluded", bd.ExcludedByUser))
			}
			if bd.Protected > 0 {
				parts = append(parts, fmt.Sprintf("%d protected", bd.Protected))
			}
			b.WriteString(styles.Dim.Render(" (" + strings.Join(parts, ", ") + ")"))
		}

		if len(r.Errors) > 0 && !m.ShowErrors {
			if len(r.Errors) == 1 {
				b.WriteString(styles.Error.Render(" (" + r.Errors[0].Error() + ")"))
			} else {
				b.WriteString(styles.Error.Render(fmt.Sprintf(" (%d errors)", len(r.Errors))))
			}
		}
		b.WriteString("\n")

		if m.ShowErrors {
			for _, e := range r.Errors {
				b.WriteString(styles.Error.Render("        " + e.Error()))
				b.WriteString("\n")
			}
		}
	}

	// A category the user deselected down to zero entries never reaches
	// Clean at all -- App.SelectedResults drops it from the plan before
	// cleaning starts, so it has no CleanResult and would otherwise vanish
	// from this screen entirely, silently. Breakdown still knows about it
	// (captured before that filtering ran), so render it explicitly rather
	// than let "I deselected everything in a category" look identical to
	// "this category was never scanned".
	rendered := make(map[cleaner.Category]bool, len(m.Results))
	for _, r := range m.Results {
		if r != nil {
			rendered[r.Category] = true
		}
	}
	var nothingSelected []cleaner.Category
	for cat, bd := range m.Breakdown {
		if rendered[cat] || bd.ExcludedByUser == 0 {
			continue
		}
		nothingSelected = append(nothingSelected, cat)
	}
	sort.Slice(nothingSelected, func(i, j int) bool { return nothingSelected[i] < nothingSelected[j] })
	for _, cat := range nothingSelected {
		line := fmt.Sprintf("  %-22s %12s %10s", cat.DisplayName(), "—", "—")
		b.WriteString(styles.Dim.Render(line))
		b.WriteString(styles.Warning.Render(" (nothing selected)"))
		b.WriteString("\n")
	}

	b.WriteString(styles.Dim.Render("  " + strings.Repeat("─", 46)))
	b.WriteString("\n")

	totalLine := fmt.Sprintf("  %-22s %12s %10d", "Total", utils.FormatBytes(m.TotalFreed), m.TotalFiles)
	b.WriteString(styles.Success.Render(totalLine))
	b.WriteString("\n\n")
	if m.Celebration != "" {
		b.WriteString(styles.Success.Render("  " + m.Celebration))
		b.WriteString("\n\n")
	}

	b.WriteString(styles.Dim.Render(fmt.Sprintf("  Time elapsed: %s", m.TotalTime.Round(time.Millisecond))))
	b.WriteString("\n")

	if m.ErrorCount > 0 {
		b.WriteString(styles.Warning.Render(fmt.Sprintf("  %d errors occurred (permission denied, etc.)", m.ErrorCount)))
		b.WriteString("\n")
	}

	if m.DryRun {
		b.WriteString("\n")
		b.WriteString(styles.Warning.Render("  Run 'tidymymac execute' to actually delete these files."))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	helpText := "  Press enter to re-run or q to quit"
	if m.ErrorCount > 0 {
		if m.ShowErrors {
			helpText += " | e to hide error details"
		} else {
			helpText += " | e to show error details"
		}
	}
	b.WriteString(styles.Help.Render(helpText))

	return b.String()
}
