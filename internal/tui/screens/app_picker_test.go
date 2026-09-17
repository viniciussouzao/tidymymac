package screens

import (
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func testApps() []cleaner.AppTarget {
	return []cleaner.AppTarget{
		{BundlePath: "/Applications/Foo.app", BundleID: "com.acme.foo", Name: "Foo"},
		{BundlePath: "/Applications/Bar.app", BundleID: "com.acme.bar", Name: "Bar"},
		{BundlePath: "/Applications/Baz.app", BundleID: "com.acme.baz", Name: "Baz"},
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

	m.ScrollDown() // cursor now at index 1 ("Bar")

	got, ok := m.Selected()
	if !ok {
		t.Fatalf("Selected() ok = false, want true")
	}
	if got != apps[1] {
		t.Fatalf("Selected() = %+v, want %+v", got, apps[1])
	}

	m.ScrollDown() // cursor now at index 2 ("Baz")
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
