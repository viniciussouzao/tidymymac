package cleaner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/safety"
)

type fakeProcessChecker struct {
	running bool
	err     error
	calls   int
	target  safety.ProcessTarget
}

func (f *fakeProcessChecker) IsRunning(_ context.Context, target safety.ProcessTarget) (bool, error) {
	f.calls++
	f.target = target
	return f.running, f.err
}

// uninstallerWithFixture builds an uninstaller plus two real on-disk entries so
// each test can assert whether anything was actually removed.
func uninstallerWithFixture(t *testing.T, checker safety.ProcessChecker) (*AppUninstaller, []FileEntry, string, string) {
	t.Helper()

	dir := t.TempDir()
	filePath := createSparseFile(t, dir, "com.acme.editor.plist", 1024)
	dirPath := filepath.Join(dir, "com.acme.editor")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, dirPath, "payload.bin", 2048)

	c := NewAppUninstaller(AppTarget{
		BundlePath: "/Applications/Acme Editor.app",
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})
	c.SetProcessChecker(checker)

	entries := []FileEntry{
		{Path: filePath, Size: 1024, Category: CategoryAppUninstall},
		{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryAppUninstall},
	}
	return c, entries, filePath, dirPath
}

func assertStillOnDisk(t *testing.T, paths ...string) {
	t.Helper()

	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q must not have been removed: %v", path, err)
		}
	}
}

func TestAppUninstallerCleanSkipsWhenAppIsRunning(t *testing.T) {
	checker := &fakeProcessChecker{running: true}
	c, entries, filePath, dirPath := uninstallerWithFixture(t, checker)

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if !result.Skipped {
		t.Error("Skipped = false, want true when the app is running")
	}
	if result.SkipReason == "" {
		t.Error("SkipReason is empty")
	}
	if !strings.Contains(result.SkipReason, "Acme Editor") {
		t.Errorf("SkipReason = %q, want it to name the app", result.SkipReason)
	}
	if result.FilesDeleted != 0 || result.BytesFreed != 0 {
		t.Errorf("FilesDeleted = %d, BytesFreed = %d, want 0 and 0", result.FilesDeleted, result.BytesFreed)
	}
	assertStillOnDisk(t, filePath, dirPath)

	if checker.target.BundlePath != "/Applications/Acme Editor.app" || checker.target.BundleID != "com.acme.editor" {
		t.Errorf("process target = %+v, want the uninstall target", checker.target)
	}
}

func TestAppUninstallerCleanFailsClosedWhenCheckErrors(t *testing.T) {
	wantErr := errors.New("ps exploded")
	checker := &fakeProcessChecker{err: wantErr}
	c, entries, filePath, dirPath := uninstallerWithFixture(t, checker)

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if len(result.Errors) != 1 || !errors.Is(result.Errors[0], wantErr) {
		t.Fatalf("Errors = %v, want the check failure", result.Errors)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0: a failed check is not 'not running'", result.FilesDeleted)
	}
	assertStillOnDisk(t, filePath, dirPath)
}

func TestAppUninstallerCleanProceedsWhenAppIsNotRunning(t *testing.T) {
	checker := &fakeProcessChecker{running: false}
	c, entries, filePath, dirPath := uninstallerWithFixture(t, checker)

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if result.Skipped {
		t.Errorf("Skipped = true (%q), want false", result.SkipReason)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none", result.Errors)
	}
	if result.FilesDeleted != 2 || result.BytesFreed != 1024+2048 {
		t.Errorf("FilesDeleted = %d, BytesFreed = %d, want 2 and %d", result.FilesDeleted, result.BytesFreed, 1024+2048)
	}
	for _, path := range []string{filePath, dirPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q should have been deleted (stat err = %v)", path, err)
		}
	}
	if checker.calls != 1 {
		t.Errorf("IsRunning called %d times, want exactly 1", checker.calls)
	}
}

// Design decision recorded by this test: a dry run never consults the process
// checker. It removes nothing by definition, so the guard has nothing to
// protect, and shelling out to ps on every preview would be wasted work.
// Early warning for a running app is the job of TargetIsRunning, which the CLI
// and the TUI call before the confirmation screen.
func TestAppUninstallerCleanDryRunDoesNotConsultProcessChecker(t *testing.T) {
	checker := &fakeProcessChecker{running: true}
	c, entries, filePath, dirPath := uninstallerWithFixture(t, checker)

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if checker.calls != 0 {
		t.Errorf("IsRunning called %d times during a dry run, want 0", checker.calls)
	}
	if result.Skipped {
		t.Errorf("dry run Skipped = true (%q), want false", result.SkipReason)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2 reported for the dry run", result.FilesDeleted)
	}
	assertStillOnDisk(t, filePath, dirPath)
}

func TestAppUninstallerTargetIsRunning(t *testing.T) {
	checker := &fakeProcessChecker{running: true}
	c := NewAppUninstaller(AppTarget{
		BundlePath: "/Applications/Acme Editor.app",
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})
	c.SetProcessChecker(checker)

	running, err := c.TargetIsRunning(t.Context())
	if err != nil {
		t.Fatalf("TargetIsRunning() error: %v", err)
	}
	if !running {
		t.Error("TargetIsRunning() = false, want true")
	}
	if checker.target.BundlePath != "/Applications/Acme Editor.app" {
		t.Errorf("process target = %+v, want the uninstall target", checker.target)
	}
}

func TestAppUninstallerTargetIsRunningDefaultsChecker(t *testing.T) {
	// A zero-value uninstaller must still be safe to use: setDefaults wires the
	// real checker, and an empty BundlePath means "nothing to check".
	c := &AppUninstaller{target: AppTarget{BundleID: "com.acme.editor"}}

	running, err := c.TargetIsRunning(t.Context())
	if err != nil {
		t.Fatalf("TargetIsRunning() error: %v", err)
	}
	if running {
		t.Error("TargetIsRunning() = true, want false with an empty bundle path")
	}
}
