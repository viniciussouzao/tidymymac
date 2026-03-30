package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

type ScanningCategory struct {
	Name      string
	Category  cleaner.Category
	Status    string // "scanning", "done", "error" or "skipped"
	Files     int
	Bytes     int64
	SizeKnown bool
	Result    *cleaner.ScanResult
	Error     error
}

type ScanningModel struct {
	Categories []ScanningCategory
	Spinner    spinner.Model
	Width      int
	Height     int
}

// NewScanning creates a new scanning model for the given category IDs
func NewScanning(selectedIDs []string, registry *cleaner.Registry) ScanningModel {
	s := spinner.New()
	s.Spinner = spinner.Dot

	var categories []ScanningCategory
	for _, id := range selectedIDs {
		c, ok := registry.Get(cleaner.Category(id))
		if !ok {
			continue
		}

		categories = append(categories, ScanningCategory{
			Name:     c.Name(),
			Category: c.Category(),
			Status:   "scanning",
		})
	}

	return ScanningModel{
		Categories: categories,
		Spinner:    s,
	}
}

// UpdateScanResult updates the scanning result for a specific category
func (m *ScanningModel) UpdateScanResult(category cleaner.Category, result *cleaner.ScanResult, err error) {
	for i := range m.Categories {
		if m.Categories[i].Category == category {
			if err != nil {
				m.Categories[i].Status = "error"
				m.Categories[i].Error = err
			} else {
				sizeKnown := true
				if result.Category == cleaner.CategoryTimeMachineSnapshots {
					sizeKnown = result.SizeKnown || result.TotalFiles == 0
				}
				m.Categories[i].Status = "done"
				m.Categories[i].Result = result
				m.Categories[i].Files = result.TotalFiles
				m.Categories[i].Bytes = result.TotalSize
				m.Categories[i].SizeKnown = sizeKnown
			}
			return
		}
	}
}

// AllDone returns true when all categories have finished scanning (either done, error or skipped)
func (m ScanningModel) AllDone() bool {
	for _, c := range m.Categories {
		if c.Status == "scanning" {
			return false
		}
	}

	return true
}

// Results return scan results for all completed categories
func (m ScanningModel) Results() map[cleaner.Category]*cleaner.ScanResult {
	results := make(map[cleaner.Category]*cleaner.ScanResult)
	for _, c := range m.Categories {
		if c.Result != nil {
			results[c.Category] = c.Result
		}
	}

	return results
}

// SetSize updates the dimensions
func (m *ScanningModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

func (m ScanningModel) Update(msg tea.Msg) (ScanningModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.Spinner, cmd = m.Spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m ScanningModel) View() string {
	var b strings.Builder

	b.WriteString(styles.Title.Render("Scanning selected categories..."))
	b.WriteString("\n\n")

	for _, cat := range m.Categories {
		var icon, sizeText string
		switch cat.Status {
		case "scanning":
			icon = m.Spinner.View()
			sizeText = styles.Dim.Render("scanning...")
		case "done":
			icon = styles.Success.Render("✓")
			if cat.Files > 0 && !cat.SizeKnown {
				sizeText = fmt.Sprintf("unknown (%d snapshots)", cat.Files)
			} else {
				sizeText = utils.FormatBytes(cat.Bytes)
				if cat.Files > 0 {
					sizeText = fmt.Sprintf("%s (%d files)", sizeText, cat.Files)
				}
			}
			sizeText = styles.Success.Render(sizeText)
		case "error":
			icon = styles.Error.Render("✗")
			sizeText = styles.Error.Render("error")
		case "skipped":
			icon = styles.Dim.Render("-")
			sizeText = styles.Dim.Render("skipped")
		}

		line := fmt.Sprintf("  %s %-22s %s", icon, cat.Name, sizeText)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")

	if m.AllDone() {
		b.WriteString(styles.Help.Render("  Press enter to review  |  q to quit"))
	} else {
		b.WriteString(styles.Help.Render("  Scanning in progress...  |  q to quit"))
	}

	return b.String()
}
