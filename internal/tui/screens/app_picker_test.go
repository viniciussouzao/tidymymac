package screens

import (
	"fmt"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// testApps returns fixtures already in the alphabetical order NewAppPicker
// sorts to (case-insensitive by Name), so index-based assertions elsewhere
// in this file don't need to account for reordering.
func testApps() []cleaner.AppTarget {
	return []cleaner.AppTarget{
		{BundlePath: "/Applications/Bar.app", BundleID: "com.acme.bar", Name: "Bar"},
		{BundlePath: "/Applications/Baz.app", BundleID: "com.acme.baz", Name: "Baz"},
		{BundlePath: "/Applications/Foo.app", BundleID: "com.acme.foo", Name: "Foo"},
	}
}

func TestAppPicker_ScrollDoesNotOverflowTop(t *testing.T) {
	m := NewAppPicker(testApps())

	m.ScrollUp()
	m.ScrollUp()
	m.ScrollUp()

	if m.Cursor != 0 {
		t.Fatalf("Cursor = %d, want 0 (clamped at top)", m.Cursor)
	}
}

func TestAppPicker_ScrollDoesNotOverflowBottom(t *testing.T) {
	m := NewAppPicker(testApps())

	for i := 0; i < 10; i++ {
		m.ScrollDown()
	}

	want := len(m.Apps) - 1
	if m.Cursor != want {
		t.Fatalf("Cursor = %d, want %d (clamped at bottom)", m.Cursor, want)
	}
}

func TestAppPicker_SelectedReturnsAppUnderCursor(t *testing.T) {
	apps := testApps()
	m := NewAppPicker(apps)

	m.ScrollDown() // cursor now at index 1 ("Baz")

	got, ok := m.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want true")
	}
	if got != apps[1] {
		t.Fatalf("Selected() = %+v, want %+v", got, apps[1])
	}

	m.ScrollDown() // cursor now at index 2 ("Foo")
	got, ok = m.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want true")
	}
	if got != apps[2] {
		t.Fatalf("Selected() = %+v, want %+v", got, apps[2])
	}
}

func TestAppPicker_EmptyList(t *testing.T) {
	m := NewAppPicker(nil)

	if _, ok := m.Selected(); ok {
		t.Fatalf("Selected() ok = true on an empty list, want false")
	}

	// ScrollUp/ScrollDown must not panic on an empty list either.
	m.ScrollUp()
	m.ScrollDown()

	var view string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("View() panicked on an empty app list: %v", r)
			}
		}()
		view = m.View()
	}()

	if !strings.Contains(view, "no third-party applications found") {
		t.Fatalf("View() = %q, want it to mention no applications were found", view)
	}
}

func TestAppPicker_ViewNonEmptyListDoesNotPanic(t *testing.T) {
	m := NewAppPicker(testApps())
	m.SetSize(80, 24)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View() panicked: %v", r)
		}
	}()

	view := m.View()
	for _, app := range testApps() {
		if !strings.Contains(view, app.Name) {
			t.Errorf("View() missing app name %q", app.Name)
		}
	}
}

// manyApps returns n fake, distinctly named apps -- enough to exceed any
// reasonable terminal height so scroll-windowing behavior can be exercised.
func manyApps(n int) []cleaner.AppTarget {
	apps := make([]cleaner.AppTarget, n)
	for i := range apps {
		apps[i] = cleaner.AppTarget{
			BundlePath: fmt.Sprintf("/Applications/App%03d.app", i),
			BundleID:   fmt.Sprintf("com.example.app%03d", i),
			Name:       fmt.Sprintf("App%03d", i),
		}
	}
	return apps
}

// countAppRows counts how many of the rendered lines in view correspond to
// an app row (as opposed to title/subtitle/help/indicator chrome), by
// counting occurrences of the distinctive "App%03d" name pattern.
func countAppRows(view string, apps []cleaner.AppTarget) int {
	count := 0
	for _, app := range apps {
		if strings.Contains(view, app.Name) {
			count++
		}
	}
	return count
}

// wantMaxTotalLines is the ceiling app_picker.go's own View() output should
// never cross for a given model: its fixed internal chrome
// (title/subtitle/help/indicators) plus however many app rows visibleRows()
// allows. This mirrors -- without duplicating -- the same budget View()
// itself is built from, so it fails loudly if that budget regresses instead
// of silently tracking whatever View() happens to do.
func wantMaxTotalLines(m AppPickerModel) int {
	// title(2) + blank(1 from "\n\n") + subtitle(1) + blank(1) + up-to-2
	// indicator lines + blank(1) + help(1), rounded up generously so this
	// stays a ceiling rather than an exact mirror of View()'s layout.
	const internalChrome = 10
	return internalChrome + m.visibleRows()
}

func TestAppPicker_ViewNeverExceedsAvailableHeight(t *testing.T) {
	apps := manyApps(100)
	m := NewAppPicker(apps)
	// A realistic terminal height, comfortably above the reserved-lines
	// fallback threshold, so this exercises the real windowing math rather
	// than the tiny-terminal fallback path.
	m.SetSize(80, 50)

	view := m.View()
	lines := strings.Split(view, "\n")

	if max := wantMaxTotalLines(m); len(lines) > max {
		t.Fatalf("View() rendered %d lines, want <= %d (internal chrome + visibleRows())", len(lines), max)
	}

	shown := countAppRows(view, apps)
	if shown == 0 {
		t.Fatalf("View() rendered no app rows at all with a 100-app list")
	}
	if shown > m.visibleRows() {
		t.Fatalf("View() rendered %d app rows, want <= visibleRows() (%d)", shown, m.visibleRows())
	}
	if shown >= len(apps) {
		t.Fatalf("View() rendered all %d apps despite Height=%d -- scroll window is not being applied", shown, m.Height)
	}
}

func TestAppPicker_ScrollFollowsCursorToEndOfList(t *testing.T) {
	apps := manyApps(100)
	m := NewAppPicker(apps)
	m.SetSize(80, 50)

	for i := 0; i < len(apps)+5; i++ {
		m.ScrollDown()
	}

	if m.Cursor != len(apps)-1 {
		t.Fatalf("Cursor = %d, want %d (clamped at bottom)", m.Cursor, len(apps)-1)
	}

	view := m.View()
	underCursor, ok := m.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false")
	}
	if !strings.Contains(view, underCursor.Name) {
		t.Fatalf("View() does not show the app under the cursor (%q) after scrolling to the end of a 100-app list -- ScrollPos is not tracking the cursor", underCursor.Name)
	}

	lines := strings.Split(view, "\n")
	if max := wantMaxTotalLines(m); len(lines) > max {
		t.Fatalf("View() rendered %d lines after scrolling to the end, want <= %d", len(lines), max)
	}
}

func TestAppPicker_ScrollFollowsCursorBackToTop(t *testing.T) {
	apps := manyApps(100)
	m := NewAppPicker(apps)
	m.SetSize(80, 20)

	for i := 0; i < len(apps)+5; i++ {
		m.ScrollDown()
	}
	for i := 0; i < len(apps)+5; i++ {
		m.ScrollUp()
	}

	if m.Cursor != 0 {
		t.Fatalf("Cursor = %d, want 0 (clamped at top)", m.Cursor)
	}

	view := m.View()
	underCursor, ok := m.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false")
	}
	if !strings.Contains(view, underCursor.Name) {
		t.Fatalf("View() does not show the app under the cursor (%q) after scrolling back to the top", underCursor.Name)
	}
}

func TestNewAppPicker_SortsByNameCaseInsensitive(t *testing.T) {
	apps := []cleaner.AppTarget{
		{Name: "ChatGPT Classic", BundleID: "com.example.chatgptclassic"},
		{Name: "ChatGPT", BundleID: "com.example.chatgpt"},
		{Name: "zoom", BundleID: "com.example.zoom"},
		{Name: "Alfred", BundleID: "com.example.alfred"},
	}

	m := NewAppPicker(apps)

	want := []string{"Alfred", "ChatGPT", "ChatGPT Classic", "zoom"}
	got := make([]string, len(m.Apps))
	for i, app := range m.Apps {
		got[i] = app.Name
	}

	if len(got) != len(want) {
		t.Fatalf("NewAppPicker() produced %d apps, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NewAppPicker() order = %v, want %v (mismatch at index %d)", got, want, i)
		}
	}
}

func TestAppPicker_ZeroHeightDoesNotPanicAndShowsContent(t *testing.T) {
	m := NewAppPicker(manyApps(50))
	// Height deliberately left at its zero value, simulating View() being
	// called before the first tea.WindowSizeMsg arrives.

	var view string
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("View() panicked with Height == 0: %v", r)
		}
	}()
	view = m.View()

	if !strings.Contains(view, "App000") {
		t.Fatalf("View() with Height == 0 did not show the first app; got: %q", view)
	}
}
