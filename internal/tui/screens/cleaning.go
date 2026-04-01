package screens

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// CleaningCategory represents the state of a single category during the cleaning process
type CleaningCategory struct {
	Name         string
	Category     cleaner.Category
	Status       string // "pending", "cleaning", "done", "error", "skipped"
	BytesTotal   int64
	BytesDeleted int64
	FilesTotal   int
	FilesDeleted int
	Result       *cleaner.CleanResult
	Error        error
	Entries      []cleaner.FileEntry
	SkipReason   string
	CurrentFile  string
	StartedAt    time.Time
}

// CleaningModel is the screen shown during the cleaning process, showing progress for each category and overall.
type CleaningModel struct {
	Categories             []CleaningCategory
	OverallBar             progress.Model
	CurrentIdx             int
	TotalTarget            int64
	TotalFreed             int64
	Done                   bool
	DryRun                 bool
	Width                  int
	Height                 int
	StartedAt              time.Time
	FinishedAt             time.Time
	CurrentCategoryStarted time.Time
	ActivityFrame          string
}

// NewCleaningModel initializes the cleaning model with the scan results and sets up the progress bar.
func NewCleaningModel(results map[cleaner.Category]*cleaner.ScanResult, dryRun bool) CleaningModel {
	bar := progress.New(progress.WithDefaultGradient())

	var categories []CleaningCategory
	var totalTarget int64

	for _, result := range results {
		if result.TotalFiles == 0 {
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
		StartedAt:   time.Now(),
		CurrentIdx:  -1,
	}
}

// NextCategory moves to the next category in the list and updates the status.
func (m *CleaningModel) NextCategory() *CleaningCategory {
	for i := range m.Categories {
		if m.Categories[i].Status == "pending" {
			m.Categories[i].Status = "cleaning"
			m.CurrentIdx = i
			m.Categories[i].StartedAt = time.Now()
			m.CurrentCategoryStarted = m.Categories[i].StartedAt
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
			m.Categories[i].CurrentFile = ""
			break
		}
	}

	m.TotalFreed = m.totalFreed()

	m.updateDoneState()
}

// SkipCategory marks a category as intentionally skipped and keeps the flow moving.
func (m *CleaningModel) SkipCategory(category cleaner.Category, reason string) {
	for i := range m.Categories {
		if m.Categories[i].Category != category {
			continue
		}
		m.Categories[i].Status = "skipped"
		m.Categories[i].SkipReason = reason
		m.Categories[i].CurrentFile = ""
		break
	}

	m.TotalFreed = m.totalFreed()
	m.updateDoneState()
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
		m.Categories[i].CurrentFile = progress.CurrentFile
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
			var errs []error
			if c.Error != nil {
				errs = []error{c.Error}
			}
			results = append(results, &cleaner.CleanResult{
				Category: c.Category,
				Errors:   errs,
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

// SetActivityFrame updates the spinner frame shown for the active category.
func (m *CleaningModel) SetActivityFrame(frame string) {
	m.ActivityFrame = frame
}

func (m *CleaningModel) updateDoneState() {
	m.Done = true
	for _, c := range m.Categories {
		if c.Status == "pending" || c.Status == "cleaning" {
			m.Done = false
			break
		}
	}

	if m.Done && m.FinishedAt.IsZero() {
		m.FinishedAt = time.Now()
	}
}

func (m CleaningModel) totalFreed() int64 {
	var total int64
	for _, cat := range m.Categories {
		total += cat.BytesDeleted
	}
	return total
}

func (m CleaningModel) overallExecutionProgress() float64 {
	if len(m.Categories) == 0 {
		return 1
	}

	completed := 0.0
	for _, cat := range m.Categories {
		switch cat.Status {
		case "done", "error", "skipped":
			completed += 1
		case "cleaning":
			completed += categoryProgress(cat)
		}
	}

	return completed / float64(len(m.Categories))
}

func categoryProgress(cat CleaningCategory) float64 {
	if cat.FilesTotal > 0 {
		progress := float64(cat.FilesDeleted) / float64(cat.FilesTotal)
		if progress < 0 {
			return 0
		}
		if progress > 1 {
			return 1
		}
		return progress
	}
	if cat.BytesTotal > 0 {
		progress := float64(cat.BytesDeleted) / float64(cat.BytesTotal)
		if progress < 0 {
			return 0
		}
		if progress > 1 {
			return 1
		}
		return progress
	}
	return 0
}

func (m CleaningModel) completedCategories() int {
	completed := 0
	for _, cat := range m.Categories {
		switch cat.Status {
		case "done", "error", "skipped":
			completed++
		}
	}
	return completed
}

func truncatePath(path string, maxLen int) string {
	if maxLen <= 0 || len(path) <= maxLen {
		return path
	}

	base := filepath.Base(path)
	if len(base)+3 >= maxLen {
		if len(base) > maxLen-3 {
			base = base[len(base)-(maxLen-3):]
		}
		return "..." + base
	}

	prefixLen := maxLen - len(base) - 4
	if prefixLen < 0 {
		prefixLen = 0
	}

	return path[:prefixLen] + ".../" + base
}

func (m CleaningModel) elapsed() time.Duration {
	if !m.FinishedAt.IsZero() {
		return m.FinishedAt.Sub(m.StartedAt).Round(time.Second)
	}
	return time.Since(m.StartedAt).Round(time.Second)
}

// View renders the cleaning screen
func (m CleaningModel) View() string {
	var b strings.Builder

	action := "Cleaning"
	if m.DryRun {
		action = "Simulating cleanup (dry run)"
	}
	b.WriteString(styles.Title.Render(action + "..."))
	b.WriteString("\n\n")

	for _, cat := range m.Categories {
		var icon, detail string

		switch cat.Status {
		case "pending":
			icon = styles.Dim.Render("○")
			detail = styles.Dim.Render("pending...")
		case "cleaning":
			icon = m.ActivityFrame
			if icon == "" {
				icon = "⟳"
			}
			pct := float64(0)
			if cat.BytesTotal > 0 {
				pct = float64(cat.BytesDeleted) / float64(cat.BytesTotal) * 100
			}
			elapsed := time.Duration(0)
			if !cat.StartedAt.IsZero() {
				elapsed = time.Since(cat.StartedAt).Round(time.Second)
			}
			detail = fmt.Sprintf("%s / %s (%.0f%%) • %d/%d files • %s",
				utils.FormatBytes(cat.BytesDeleted),
				utils.FormatBytes(cat.BytesTotal),
				pct,
				cat.FilesDeleted,
				cat.FilesTotal,
				elapsed,
			)
		case "done":
			icon = styles.Success.Render("✓")
			detail = styles.Success.Render(fmt.Sprintf("%s freed (%d files)",
				utils.FormatBytes(cat.BytesDeleted),
				cat.FilesDeleted,
			))
		case "skipped":
			icon = styles.Warning.Render("!")
			detail = styles.Warning.Render("skipped")
		case "error":
			icon = styles.Error.Render("✗")
			detail = styles.Error.Render("error")
		}

		line := fmt.Sprintf("  %s  %-22s %s", icon, cat.Name, detail)
		b.WriteString(line)
		b.WriteString("\n")
		if cat.Status == "cleaning" && cat.CurrentFile != "" {
			b.WriteString(styles.Dim.Render("      Working on: " + truncatePath(cat.CurrentFile, 72)))
			b.WriteString("\n")
		}
		if cat.Status == "skipped" && cat.SkipReason != "" {
			b.WriteString(styles.Dim.Render("      " + cat.SkipReason))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Overall progress.
	if len(m.Categories) > 0 {
		pct := m.overallExecutionProgress()
		b.WriteString(styles.Subtitle.Render(fmt.Sprintf("  Execution: %d/%d categories complete",
			m.completedCategories(),
			len(m.Categories),
		)))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(m.OverallBar.ViewAs(pct))
		b.WriteString("\n")
		b.WriteString("\n")
		if m.TotalTarget > 0 {
			b.WriteString(styles.Subtitle.Render(fmt.Sprintf("  Reclaimed so far: %s / %s targeted",
				utils.FormatBytes(m.TotalFreed),
				utils.FormatBytes(m.TotalTarget),
			)))
			b.WriteString("\n")
		} else {
			b.WriteString(styles.Subtitle.Render(fmt.Sprintf("  Reclaimed so far: %s", utils.FormatBytes(m.TotalFreed))))
			b.WriteString("\n")
		}
		b.WriteString(styles.Muted.Render(fmt.Sprintf("  Elapsed: %s", m.elapsed())))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	if m.Done {
		b.WriteString(styles.Help.Render("  Press enter to see summary  |  q to quit"))
	} else {
		b.WriteString(styles.Help.Render("  Cleaning in progress... please wait"))
	}

	return b.String()
}
