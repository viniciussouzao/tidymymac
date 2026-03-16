package screens

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// CategoryItem represents a cleanup category in the main dashboard
type CategoryItem struct {
	ID   string
	Name string
	Desc string
	//Icon      string // for future use, maybe we can add emojis or something to make it more visually appealing
	Selected  bool
	Size      int64 // in bytes, -1 means not scanned yet
	Scanning  bool
	NeedsSudo bool
}

// DashboardModel is the main screen where users select categories
type DashboardModel struct {
	Categories []CategoryItem
	Cursor     int
	Width      int
	Height     int
	DiskTotal  int64
	DiskUsed   int64
	ShowAll    bool // when false, only show categories that have been scanned and have size > 0
}

// NewDashboard initializes the dashboard with all categories and default values
func NewDashboard() DashboardModel {
	m := DashboardModel{
		Categories: []CategoryItem{
			{ID: "temp", Name: "Temp Files", Desc: "System and user temporary files", Size: -1, NeedsSudo: true},
		},
	}

	if total, used, _, err := utils.DiskUsage("/"); err == nil {
		m.DiskTotal = total
		m.DiskUsed = used
	}

	return m
}

type DashboardMsg struct {
	Selected []string
}

func (m DashboardModel) HandleKey(keyStr, keyType string) (DashboardModel, interface{}) {
	visible := m.visibleIndices()

	switch {
	case keyType == "up" || keyType == "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case keyType == "down" || keyType == "j":
		if m.Cursor < len(m.Categories)-1 {
			m.Cursor++
		}
	case keyStr == " " || keyStr == "x":
		if m.Cursor < len(visible) {
			// Toggle selection of the currently highlighted category
			m.Categories[visible[m.Cursor]].Selected = !m.Categories[visible[m.Cursor]].Selected
		}
	case keyStr == "a":
		allSelected := true
		for _, idx := range visible {
			if !m.Categories[idx].Selected {
				allSelected = false
				break
			}
		}
		for _, idx := range visible {
			m.Categories[idx].Selected = !allSelected
		}

	case keyStr == "v":
		m.ShowAll = !m.ShowAll

		newVisible := m.visibleIndices()
		if m.Cursor >= len(newVisible) {
			m.Cursor = len(newVisible) - 1
		}

		if m.Cursor < 0 {
			m.Cursor = 0
		}
	case keyStr == "enter":
		var selected []string
		for _, idx := range visible {
			if m.Categories[idx].Selected {
				selected = append(selected, m.Categories[idx].ID)
			}
		}
		return m, DashboardMsg{Selected: selected}
	}

	return m, nil
}

// SetCategoryScanning marks a category as currently being scanned, which the UI can use to show a "Scanning..." status
func (m *DashboardModel) SetCategoryScanning(id string) {
	for i := range m.Categories {
		if m.Categories[i].ID == id {
			m.Categories[i].Size = -1 // reset size so the UI shows "Scanning..."
			m.Categories[i].Scanning = true
		}
	}
}

// UpdateCategorySize updates the scanned size for a category.
func (m *DashboardModel) UpdateCategorySize(id string, size int64) {
	for i := range m.Categories {
		if m.Categories[i].ID == id {
			m.Categories[i].Size = size
			m.Categories[i].Scanning = false
			return
		}
	}
}

// SelectedCount returns the number of selected categories
func (m *DashboardModel) SelectedCount() int {
	n := 0
	for _, c := range m.Categories {
		if c.Selected {
			n++
		}
	}

	return n
}

// visibleIndices returns the indices of categories that should be shown.
// When ShowAll is false, categories that have been scanned and have 0 bytes are hidden.
func (m DashboardModel) visibleIndices() []int {
	if m.ShowAll {
		indices := make([]int, len(m.Categories))
		for i := range m.Categories {
			indices[i] = i
		}
		return indices
	}
	var indices []int
	for i, category := range m.Categories {
		if category.Size != 0 { // show if not yet scanned (-1) or has content (>0)
			indices = append(indices, i)
		}
	}
	return indices
}

// View renders the dashboard screen(bubbletea style) - this is where you would implement the actual TUI rendering logic
func (m DashboardModel) View() string {
	var b strings.Builder

	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Margin(1)

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))

	selectedColorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)

	cursorColorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	sizeGreenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))

	sizeYellowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))

	sizeRedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)

	// Disk usage summary bar
	if m.DiskTotal > 0 {
		pct := int(math.Round(float64(m.DiskUsed) / float64(m.DiskTotal) * 100))
		const barWidth = 35
		filled := int(math.Round(float64(pct) / 100 * barWidth))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

		var barStyle lipgloss.Style
		switch {
		case pct >= 85:
			barStyle = sizeRedStyle
		case pct >= 70:
			barStyle = sizeYellowStyle
		default:
			barStyle = sizeGreenStyle
		}

		storageStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAAA"))
		b.WriteString(fmt.Sprintf("  %s  %s\n\n",
			barStyle.Render(bar),
			storageStyle.Render(fmt.Sprintf("%s used of %s (%d%%)", utils.FormatBytes(m.DiskUsed), utils.FormatBytes(m.DiskTotal), pct)),
		))
	}

	// Title and instructions
	b.WriteString(lipgloss.NewStyle().Bold(true).Underline(true).Render("Review and select categories to clean"))
	b.WriteString("\n\n")

	// Category list
	visible := m.visibleIndices()
	for visIdx, catIdx := range visible {
		cat := m.Categories[catIdx]

		cursor := "  "
		if visIdx == m.Cursor {
			cursor = cursorColorStyle.Render("> ")
		}

		checkbox := "[ ]"
		if cat.Selected {
			checkbox = selectedColorStyle.Render("[x]")
		}

		name := cat.Name
		if visIdx == m.Cursor {
			name = cursorColorStyle.Render(name)
		}

		sizeText := dimStyle.Render("scanning...")
		if cat.Size >= 0 {
			formmated := utils.FormatBytes(cat.Size)
			switch {
			case cat.Size >= 1<<30: // 1GB
				sizeText = sizeRedStyle.Render(formmated)
			case cat.Size >= 100<<20: // 100MB
				sizeText = sizeYellowStyle.Render(formmated)
			default:
				sizeText = sizeGreenStyle.Render(formmated)
			}
		} else if cat.Scanning {
			sizeText = dimStyle.Render("scanning...")
		} else {
			sizeText = dimStyle.Render("-")
		}

		sudoTag := ""
		if cat.NeedsSudo {
			sudoTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Render(" [sudo]")
		}

		desc := dimStyle.Render(cat.Desc)

		line := fmt.Sprintf("%s %s %-22s %s  %s%s", cursor, checkbox, name, sizeText, desc, sudoTag)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")

	var totalFreelable int64
	var selectedFreelable int64
	stillScanning := false
	stillScanningSelected := false

	for _, cat := range m.Categories {
		if cat.Scanning {
			stillScanning = true
			if cat.Selected {
				stillScanningSelected = true
			}
		} else if cat.Size > 0 {
			totalFreelable += cat.Size
			if cat.Selected {
				selectedFreelable += cat.Size
			}
		}
	}

	freeableStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#10B981"))
	scanNote := ""
	if stillScanning {
		scanNote = " (scanning...)"
	}

	b.WriteString(fmt.Sprintf("  Total freeable: %s%s\n", freeableStyle.Render(utils.FormatBytes(totalFreelable)), dimStyle.Render(scanNote)))

	selectedScanNote := ""
	if stillScanningSelected {
		selectedScanNote = " (scanning...)"
	}

	b.WriteString(fmt.Sprintf("  Selected freeable: %s%s\n", freeableStyle.Render(utils.FormatBytes(selectedFreelable)), dimStyle.Render(selectedScanNote)))
	b.WriteString("\n")

	viewToggleLabel := "v: show all"
	if m.ShowAll {
		viewToggleLabel = "v: hide empty"
	}

	selectedCount := m.SelectedCount()
	if selectedCount > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf(
			"  space: toggle  a: select all  %s  r: re-run  enter: scan %d selected  q: quit",
			viewToggleLabel, selectedCount,
		)))
	} else {
		b.WriteString(helpStyle.Render(fmt.Sprintf(
			"  space: toggle  a: select all  %s  r: re-run  q: quit",
			viewToggleLabel,
		)))
	}

	return b.String()
}

func (m *DashboardModel) SetSize(w, d int) {
	m.Width = w
	m.Height = d
}
