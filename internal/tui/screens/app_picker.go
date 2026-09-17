package screens

import (
	"fmt"
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
	Apps   []cleaner.AppTarget
	Cursor int
	Width  int
	Height int
}

// NewAppPicker builds a picker over the given discovered applications. apps
// may be empty (nothing found, or discovery failed) -- View handles that case
// explicitly rather than assuming a non-empty list.
func NewAppPicker(apps []cleaner.AppTarget) AppPickerModel {
	return AppPickerModel{Apps: apps}
}

// ScrollUp moves the cursor up one row, clamped to the top of the list.
func (m *AppPickerModel) ScrollUp() {
	if m.Cursor > 0 {
		m.Cursor--
	}
}

// ScrollDown moves the cursor down one row, clamped to the bottom of the
// list.
func (m *AppPickerModel) ScrollDown() {
	if m.Cursor < len(m.Apps)-1 {
		m.Cursor++
	}
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
// explicit message rather than an empty list with no explanation.
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

	for i, app := range m.Apps {
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

	b.WriteString("\n")
	b.WriteString(styles.Help.Render("  up/down: navigate  enter: select  q: quit"))

	return b.String()
}
