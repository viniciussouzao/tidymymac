package screens

import (
	"fmt"
	"sort"
	"strings"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
)

// AppPickerModel lets the user choose a single installed application to run
// Smart Uninstall against, from the list cleaner.DiscoverInstalledApps found.
// Unlike ReviewModel, this is single-select -- there is no checkbox, just a
// cursor and one confirmed choice -- because uninstalling targets exactly one
// application per session.
type AppPickerModel struct {
	Apps      []cleaner.AppTarget
	Cursor    int
	ScrollPos int
	Width     int
	Height    int
}

// appPickerReservedLines is how much vertical space View() assumes is
// already spoken for by things it cannot see or does not itself render on
// every frame: App.View()'s global ASCII logo header + tagline (up to 15
// rows), the dry-run banner App.View() prepends when not in execute mode (up
// to 4 more rows -- picker doesn't know the run mode, so this reserves for
// the worst case), this screen's own title/subtitle/help chrome (8 rows),
// and the "N more above/below" indicators this screen may add when the list
// is scrolled (up to 2 rows). Reserving the sum up front means the number of
// app rows View() actually prints, plus this budget, never exceeds
// m.Height -- which is what was missing before and let BubbleTea's
// cursor-relative redraw desync into visible garbage on machines with dozens
// of installed apps.
const appPickerReservedLines = 30

// NewAppPicker builds a picker over the given discovered applications. apps
// may be empty (nothing found, or discovery failed) -- View handles that case
// explicitly rather than assuming a non-empty list. The list is sorted by
// display name (case-insensitive), matching `tidymymac uninstall --list`, so
// the two views never disagree on ordering.
func NewAppPicker(apps []cleaner.AppTarget) AppPickerModel {
	sorted := make([]cleaner.AppTarget, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	return AppPickerModel{Apps: sorted}
}

// visibleRows returns how many app rows View() may render given the current
// Height, always leaving room for appPickerReservedLines. Height is zero
// before the first tea.WindowSizeMsg arrives, and the resulting negative
// budget falls back to a fixed value rather than rendering nothing.
func (m AppPickerModel) visibleRows() int {
	viewHeight := m.Height - appPickerReservedLines
	if viewHeight < 5 {
		viewHeight = 20
	}
	return viewHeight
}

// syncScroll adjusts ScrollPos so the row under Cursor stays within the
// window visibleRows() will actually render, mirroring the scroll-follows-
// cursor behavior ReviewModel.scrollIntoView uses.
func (m *AppPickerModel) syncScroll() {
	viewHeight := m.visibleRows()

	if m.Cursor < m.ScrollPos {
		m.ScrollPos = m.Cursor
	} else if m.Cursor >= m.ScrollPos+viewHeight {
		m.ScrollPos = m.Cursor - viewHeight + 1
	}

	if m.ScrollPos < 0 {
		m.ScrollPos = 0
	}
}

// ScrollUp moves the cursor up one row, clamped to the top of the list, and
// keeps the cursor within the visible window.
func (m *AppPickerModel) ScrollUp() {
	if m.Cursor > 0 {
		m.Cursor--
	}
	m.syncScroll()
}

// ScrollDown moves the cursor down one row, clamped to the bottom of the
// list, and keeps the cursor within the visible window.
func (m *AppPickerModel) ScrollDown() {
	if m.Cursor < len(m.Apps)-1 {
		m.Cursor++
	}
	m.syncScroll()
}

// Selected returns the application currently under the cursor. ok is false
// when the list is empty or the cursor is somehow out of range.
func (m AppPickerModel) Selected() (cleaner.AppTarget, bool) {
	if m.Cursor < 0 || m.Cursor >= len(m.Apps) {
		return cleaner.AppTarget{}, false
	}
	return m.Apps[m.Cursor], true
}

// SetSize updates the dimensions.
func (m *AppPickerModel) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

// View renders the picker screen. An empty app list is rendered as an
// explicit message rather than an empty list with no explanation. The app
// list itself is windowed to visibleRows() so it never overflows the
// terminal regardless of how many applications were discovered -- see
// appPickerReservedLines.
func (m AppPickerModel) View() string {
	var b strings.Builder

	b.WriteString(styles.Title.Render("Uninstall an application"))
	b.WriteString("\n\n")

	if len(m.Apps) == 0 {
		b.WriteString(styles.Dim.Render("  no third-party applications found"))
		b.WriteString("\n\n")
		b.WriteString(styles.Help.Render("  q: quit"))
		return b.String()
	}

	b.WriteString(styles.Plain.Render("Select an application to scan for removal candidates"))
	b.WriteString("\n\n")

	viewHeight := m.visibleRows()

	start := m.ScrollPos
	if start < 0 {
		start = 0
	}
	if start > len(m.Apps) {
		start = len(m.Apps)
	}

	end := start + viewHeight
	if end > len(m.Apps) {
		end = len(m.Apps)
	}

	if start > 0 {
		b.WriteString(styles.More.Render(fmt.Sprintf("  ↑ %d more above", start)))
		b.WriteString("\n")
	}

	for i := start; i < end; i++ {
		app := m.Apps[i]
		cursor := "  "
		name := app.Name
		if i == m.Cursor {
			cursor = styles.Cursor.Render("> ")
			name = styles.Cursor.Render(name)
		}

		line := fmt.Sprintf("%s %-30s %s", cursor, name, styles.Dim.Render(app.BundleID))
		b.WriteString(line)
		b.WriteString("\n")
	}

	if remaining := len(m.Apps) - end; remaining > 0 {
		b.WriteString(styles.More.Render(fmt.Sprintf("  ↓ %d more below", remaining)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styles.Help.Render("  up/down: navigate  enter: select  q: quit"))

	return b.String()
}
