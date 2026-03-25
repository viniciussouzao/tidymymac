package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// CleaningCategory represents the state of a single category during the cleaning process
type CleaningCategory struct {
	Name         string
	Category     cleaner.Category
	Status       string // "pending", "cleaning", "done", "error"
	BytesTotal   int64
	BytesDeleted int64
	FilesTotal   int
	FilesDeleted int
	Result       *cleaner.CleanResult
	Error        error
	Entries      []cleaner.FileEntry
}

// CleaningModel is the screen shown during the cleaning process, showing progress for each category and overall.
type CleaningModel struct {
	Categories  []CleaningCategory
	OverallBar  progress.Model
	CurrentIdx  int
	TotalTarget int64
	TotalFreed  int64
	Done        bool
	DryRun      bool
	Width       int
	Height      int
}

// NewCleaningModel initializes the cleaning model with the scan results and sets up the progress bar.
func NewCleaningModel(results map[cleaner.Category]*cleaner.ScanResult, dryRun bool) CleaningModel {
	bar := progress.New(progress.WithDefaultGradient())

	var categories []CleaningCategory
	var totalTarget int64

	for _, result := range results {
		if result.TotalSize == 0 {
			continue
		}

		categories = append(categories, CleaningCategory{
			Name:       string(result.Category),
			Category:   result.Category,
			Status:     "pending",
			BytesTotal: result.TotalSize,
			FilesTotal: result.TotalFiles,
			Entries:    result.Entries,
		})
		totalTarget += result.TotalSize
	}

	return CleaningModel{
		Categories:  categories,
		OverallBar:  bar,
		TotalTarget: totalTarget,
		DryRun:      dryRun,
	}
}

// NextCategory moves to the next category in the list and updates the status.
func (m *CleaningModel) NextCategory() *CleaningCategory {
	for i := range m.Categories {
		if m.Categories[i].Status == "pending" {
			m.Categories[i].Status = "cleaning"
			m.CurrentIdx = i
			return &m.Categories[i]
		}
	}

	return nil
}

// UpdateCleanResult updates a category with its clean result
func (m *CleaningModel) UpdateCleanResult(category cleaner.Category, result *cleaner.CleanResult, err error) {
	for i := range m.Categories {
		if m.Categories[i].Category == category {
			if err != nil {
				m.Categories[i].Status = "error"
				m.Categories[i].Error = err
			} else {
				m.Categories[i].Status = "done"
				m.Categories[i].Result = result
				m.Categories[i].BytesDeleted = result.BytesFreed
				m.Categories[i].FilesDeleted = result.FilesDeleted
			}
			break
		}
	}

	m.TotalFreed = m.totalFreed()

	//check if all categories are done
	m.Done = true
	for _, c := range m.Categories {
		if c.Status == "pending" || c.Status == "cleaning" {
			m.Done = false
			break
		}
	}
}

// UpdateCleanProgress updates the currently running category with partial progress.
func (m *CleaningModel) UpdateCleanProgress(progress cleaner.CleanProgress) {
	for i := range m.Categories {
		if m.Categories[i].Category != progress.Category {
			continue
		}

		if m.Categories[i].Status == "pending" {
			m.Categories[i].Status = "cleaning"
		}
		if progress.FilesTotal > 0 {
			m.Categories[i].FilesTotal = progress.FilesTotal
		}
		if progress.BytesTotal > 0 {
			m.Categories[i].BytesTotal = progress.BytesTotal
		}
		m.Categories[i].FilesDeleted = progress.FilesDeleted
		m.Categories[i].BytesDeleted = progress.BytesDeleted
		break
	}

	m.TotalFreed = m.totalFreed()
}

// Results returns the clean results
func (m CleaningModel) Results() []*cleaner.CleanResult {
	var results []*cleaner.CleanResult
	for _, c := range m.Categories {
		if c.Result != nil {
			results = append(results, c.Result)
		} else {
			results = append(results, &cleaner.CleanResult{
				Category: c.Category,
				Errors:   []error{c.Error},
			})
		}
	}
	return results
}

// SetSize updates the model's width and height
func (m *CleaningModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

func (m CleaningModel) totalFreed() int64 {
	var total int64
	for _, cat := range m.Categories {
		total += cat.BytesDeleted
	}
	return total
}

// View renders the cleaning screen
func (m CleaningModel) View() string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF6B6B")).MarginBottom(1)

	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))

	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).MarginTop(1)

	action := "Cleaning"
	if m.DryRun {
		action = "Simulating cleanup (dry run)"
	}
	b.WriteString(titleStyle.Render(action + "..."))
	b.WriteString("\n\n")

	for _, cat := range m.Categories {
		var icon, detail string

		switch cat.Status {
		case "pending":
			icon = dimStyle.Render("○")
			detail = dimStyle.Render("pending...")
		case "cleaning":
			icon = "⟳"
			pct := float64(0)
			if cat.BytesTotal > 0 {
				pct = float64(cat.BytesDeleted) / float64(cat.BytesTotal) * 100
			}
			detail = fmt.Sprintf("%s / %s (%.0f%%)",
				utils.FormatBytes(cat.BytesDeleted),
				utils.FormatBytes(cat.BytesTotal),
				pct,
			)
		case "done":
			icon = doneStyle.Render("✓")
			detail = doneStyle.Render(fmt.Sprintf("%s freed (%d files)",
				utils.FormatBytes(cat.BytesDeleted),
				cat.FilesDeleted,
			))
		case "error":
			icon = errorStyle.Render("✗")
			detail = errorStyle.Render("error")
		}

		line := fmt.Sprintf("  %s  %-22s %s", icon, cat.Name, detail)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Overall progress.
	if m.TotalTarget > 0 {
		pct := float64(m.TotalFreed) / float64(m.TotalTarget)
		b.WriteString(fmt.Sprintf("  Overall: %s / %s\n",
			utils.FormatBytes(m.TotalFreed),
			utils.FormatBytes(m.TotalTarget),
		))
		b.WriteString("  ")
		b.WriteString(m.OverallBar.ViewAs(pct))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	if m.Done {
		b.WriteString(helpStyle.Render("  Press enter to see summary  |  q to quit"))
	} else {
		b.WriteString(helpStyle.Render("  Cleaning in progress... please wait"))
	}

	return b.String()
}
