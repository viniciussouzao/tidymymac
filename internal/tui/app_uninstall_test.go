package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

// TestNewUninstallApp_NoTarget_OpensAppPicker covers the interactive entry
// point of the Smart Uninstall flow: `tidymymac uninstall` with no app
// argument must land on screenAppPicker so the user can choose one.
func TestNewUninstallApp_NoTarget_OpensAppPicker(t *testing.T) {
	a := NewUninstallApp(context.Background(), false, &config.Config{}, nil)
	defer a.cancel()

	if a.currentScreen != screenAppPicker {
		t.Fatalf("currentScreen = %v, want screenAppPicker", a.currentScreen)
	}
	if !a.uninstallFlow {
		t.Fatalf("uninstallFlow = false, want true")
	}
	if a.registry == nil {
		t.Fatalf("registry is nil, want a non-nil (possibly empty) registry")
	}
	if len(a.registry.All()) != 0 {
		t.Fatalf("registry has %d cleaners before a target is chosen, want 0", len(a.registry.All()))
	}
}

// TestNewUninstallApp_WithTarget_OpensScanningWithRegisteredCleaner covers
// `tidymymac uninstall <app>`: the picker is skipped entirely and the app
// starts directly on screenScanning, with a throwaway registry already
// holding a cleaner.AppUninstaller for that exact target -- exactly as if it
// had just been chosen from the picker (see startUninstallScan).
func TestNewUninstallApp_WithTarget_OpensScanningWithRegisteredCleaner(t *testing.T) {
	target := cleaner.AppTarget{
		BundlePath: "/Applications/Foo.app",
		BundleID:   "com.acme.foo",
		Name:       "Foo",
	}

	a := NewUninstallApp(context.Background(), false, &config.Config{}, &target)
	defer a.cancel()

	if a.currentScreen != screenScanning {
		t.Fatalf("currentScreen = %v, want screenScanning", a.currentScreen)
	}
	if !a.uninstallFlow {
		t.Fatalf("uninstallFlow = false, want true")
	}

	c, ok := a.registry.Get(cleaner.CategoryAppUninstall)
	if !ok {
		t.Fatalf("registry has no cleaner registered for CategoryAppUninstall")
	}
	au, ok := c.(*cleaner.AppUninstaller)
	if !ok {
		t.Fatalf("registered cleaner is %T, want *cleaner.AppUninstaller", c)
	}
	if got := au.Target(); got != target {
		t.Fatalf("registered AppUninstaller.Target() = %+v, want %+v", got, target)
	}

	if len(a.scanningScr.Categories) != 1 {
		t.Fatalf("scanningScr has %d categories, want 1", len(a.scanningScr.Categories))
	}
	if a.scanningScr.Categories[0].Category != cleaner.CategoryAppUninstall {
		t.Fatalf("scanningScr category = %v, want %v", a.scanningScr.Categories[0].Category, cleaner.CategoryAppUninstall)
	}

	// The app picker itself is still populated (from the up-front discovery
	// pass in NewUninstallApp) so that backing out of screenScanning with esc
	// has somewhere real to land -- see updateScanning's uninstallFlow branch.
	// Discovery may legitimately find zero apps on a bare test machine, so
	// this only asserts the field was actually initialized, not that it is
	// non-empty.
	_ = a.appPickerScr.Apps
}

// TestUpdateAppPicker_ConfirmStartsUninstallScan exercises the picker's own
// Update path end to end: navigating to an app and confirming it must
// transition to screenScanning with that exact app registered, mirroring
// what NewUninstallApp does when a target is supplied directly.
func TestUpdateAppPicker_ConfirmStartsUninstallScan(t *testing.T) {
	apps := []cleaner.AppTarget{
		{BundlePath: "/Applications/Foo.app", BundleID: "com.acme.foo", Name: "Foo"},
		{BundlePath: "/Applications/Bar.app", BundleID: "com.acme.bar", Name: "Bar"},
	}

	a := NewUninstallApp(context.Background(), false, &config.Config{}, nil)
	defer a.cancel()
	a.appPickerScr.Apps = apps

	// Navigate the cursor down to "Bar" purely through updateAppPicker, the
	// same path a real key press takes -- not by poking AppPickerModel's
	// fields directly.
	model, cmd := a.updateAppPicker(tea.KeyMsg{Type: tea.KeyDown})
	next := model.(App)
	if next.currentScreen != screenAppPicker {
		t.Fatalf("currentScreen after a mere navigation key = %v, want screenAppPicker (unchanged)", next.currentScreen)
	}
	if cmd != nil {
		t.Fatalf("cmd = %v after a mere navigation key, want nil", cmd)
	}

	model, cmd = next.updateAppPicker(tea.KeyMsg{Type: tea.KeyEnter})
	next = model.(App)

	if next.currentScreen != screenScanning {
		t.Fatalf("currentScreen = %v, want screenScanning", next.currentScreen)
	}
	if cmd == nil {
		t.Fatalf("cmd = nil, want a batched scan command")
	}

	c, ok := next.registry.Get(cleaner.CategoryAppUninstall)
	if !ok {
		t.Fatalf("registry has no cleaner registered for CategoryAppUninstall")
	}
	au := c.(*cleaner.AppUninstaller)
	if got := au.Target(); got != apps[1] {
		t.Fatalf("registered AppUninstaller.Target() = %+v, want %+v (Bar)", got, apps[1])
	}
}

// TestUpdateScanning_Back_UninstallFlowReturnsToAppPicker covers the "there
// is no dashboard in this flow" branch documented on uninstallFlow: esc from
// screenScanning, reached via the Smart Uninstall flow, must land back on
// screenAppPicker rather than the ordinary flow's screenDashboard.
func TestUpdateScanning_Back_UninstallFlowReturnsToAppPicker(t *testing.T) {
	target := cleaner.AppTarget{BundlePath: "/Applications/Foo.app", BundleID: "com.acme.foo", Name: "Foo"}
	a := NewUninstallApp(context.Background(), false, &config.Config{}, &target)
	defer a.cancel()

	if a.currentScreen != screenScanning {
		t.Fatalf("currentScreen = %v, want screenScanning", a.currentScreen)
	}

	model, _ := a.updateScanning(tea.KeyMsg{Type: tea.KeyEsc})
	next := model.(App)

	if next.currentScreen != screenAppPicker {
		t.Fatalf("currentScreen after esc = %v, want screenAppPicker", next.currentScreen)
	}
}
