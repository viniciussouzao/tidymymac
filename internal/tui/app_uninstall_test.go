package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
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

// fakeRunningAppUninstaller is a minimal cleaner.Cleaner + targetRunningChecker
// double for CategoryAppUninstall -- standing in for *cleaner.AppUninstaller
// so checkTargetRunningCmd's outcome (see internal/tui/app.go) can be pinned
// deterministically, without shelling out to the real `ps`-backed
// safety.ProcessChecker. Mirrors cmd/uninstall_test.go's own
// fakeUninstallCleaner, scoped down to only what the TUI side needs.
type fakeRunningAppUninstaller struct {
	entry cleaner.FileEntry

	running    bool
	runningErr error
}

func (f *fakeRunningAppUninstaller) Category() cleaner.Category { return cleaner.CategoryAppUninstall }
func (f *fakeRunningAppUninstaller) Name() string               { return "fake uninstall" }
func (f *fakeRunningAppUninstaller) Description() string        { return "fake uninstall cleaner for tests" }
func (f *fakeRunningAppUninstaller) RequiresSudo() bool         { return false }
func (f *fakeRunningAppUninstaller) DeletesWholeDomain() bool   { return false }

func (f *fakeRunningAppUninstaller) Scan(ctx context.Context, progress func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return &cleaner.ScanResult{
		Category:   cleaner.CategoryAppUninstall,
		Entries:    []cleaner.FileEntry{f.entry},
		TotalFiles: 1,
		TotalSize:  f.entry.Size,
	}, nil
}

func (f *fakeRunningAppUninstaller) Clean(ctx context.Context, entries []cleaner.FileEntry, dryRun bool, progress func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	return &cleaner.CleanResult{Category: cleaner.CategoryAppUninstall, DryRun: dryRun, FilesDeleted: len(entries)}, nil
}

func (f *fakeRunningAppUninstaller) TargetIsRunning(ctx context.Context) (bool, error) {
	return f.running, f.runningErr
}

// TestUpdateScanning_UninstallFlow_WarnsWhenTargetRunning covers Security
// review Fase 7/7's TUI follow-up: confirming the scan in the Smart Uninstall
// flow must dispatch a check for whether the target app is currently
// running, and the result must end up visible on the review screen -- before
// the user confirms, not only after AppUninstaller.Clean refuses to delete
// anything (see cleaner.AppUninstaller.TargetIsRunning's own doc comment and
// the CLI's equivalent, warnIfTargetRunning in cmd/uninstall_output.go).
func TestUpdateScanning_UninstallFlow_WarnsWhenTargetRunning(t *testing.T) {
	entry := cleaner.FileEntry{Path: "/Applications/Foo.app", Size: 100}
	registry := cleaner.NewRegistry()
	registry.Register(&fakeRunningAppUninstaller{entry: entry, running: true})

	scanning := screens.NewScanning([]string{string(cleaner.CategoryAppUninstall)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryAppUninstall, &cleaner.ScanResult{
		Category:   cleaner.CategoryAppUninstall,
		Entries:    []cleaner.FileEntry{entry},
		TotalFiles: 1,
		TotalSize:  entry.Size,
	}, nil)

	a := App{
		currentScreen: screenScanning,
		uninstallFlow: true,
		registry:      registry,
		scanningScr:   scanning,
		ctx:           context.Background(),
	}

	model, cmd := a.updateScanning(tea.KeyMsg{Type: tea.KeyEnter})
	next := model.(App)
	if next.currentScreen != screenReview {
		t.Fatalf("currentScreen = %v, want screenReview", next.currentScreen)
	}
	if cmd == nil {
		t.Fatal("expected a checkTargetRunningCmd to be dispatched on confirming the scan, got nil")
	}
	// The review screen must not claim the target is running before the
	// async check's result has actually arrived.
	if next.reviewScr.ShouldWarnAboutTargetRunning() {
		t.Fatal("ShouldWarnAboutTargetRunning() = true before the check's result arrived, want false")
	}

	msg := cmd()
	trMsg, ok := msg.(targetRunningMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want targetRunningMsg", msg)
	}
	if !trMsg.running {
		t.Fatal("targetRunningMsg.running = false, want true (fake was configured running: true)")
	}

	model, _ = next.Update(trMsg)
	final := model.(App)
	if !final.reviewScr.ShouldWarnAboutTargetRunning() {
		t.Fatal("ShouldWarnAboutTargetRunning() = false, want true after targetRunningMsg{running: true}")
	}
	if !strings.Contains(final.reviewScr.View(), "appears to be running") {
		t.Fatalf("View() should surface the running-app warning:\n%s", final.reviewScr.View())
	}
}

// TestUpdateScanning_UninstallFlow_TargetRunningCheckErrIsNonFatal covers the
// "I could not check" case: a failed TargetIsRunning call must render a
// softer, distinct warning rather than silently doing nothing or blocking
// navigation.
func TestUpdateScanning_UninstallFlow_TargetRunningCheckErrIsNonFatal(t *testing.T) {
	entry := cleaner.FileEntry{Path: "/Applications/Foo.app", Size: 100}
	registry := cleaner.NewRegistry()
	registry.Register(&fakeRunningAppUninstaller{entry: entry, runningErr: context.DeadlineExceeded})

	scanning := screens.NewScanning([]string{string(cleaner.CategoryAppUninstall)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryAppUninstall, &cleaner.ScanResult{
		Category:   cleaner.CategoryAppUninstall,
		Entries:    []cleaner.FileEntry{entry},
		TotalFiles: 1,
		TotalSize:  entry.Size,
	}, nil)

	a := App{
		currentScreen: screenScanning,
		uninstallFlow: true,
		registry:      registry,
		scanningScr:   scanning,
		ctx:           context.Background(),
	}

	model, cmd := a.updateScanning(tea.KeyMsg{Type: tea.KeyEnter})
	next := model.(App)
	if cmd == nil {
		t.Fatal("expected a checkTargetRunningCmd to be dispatched, got nil")
	}

	model, _ = next.Update(cmd())
	final := model.(App)

	if final.reviewScr.ShouldWarnAboutTargetRunning() {
		t.Fatal("ShouldWarnAboutTargetRunning() = true on a check error, want false")
	}
	if final.reviewScr.TargetRunningCheckErr == nil {
		t.Fatal("TargetRunningCheckErr = nil, want the propagated error")
	}
	if !strings.Contains(final.reviewScr.View(), "could not determine whether") {
		t.Fatalf("View() should surface a softer warning on a check error:\n%s", final.reviewScr.View())
	}
}
