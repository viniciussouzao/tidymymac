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
	// real checker rather than panicking on a nil one.
	//
	// The false returned for an empty BundlePath means "no evidence", NOT
	// "safe to delete". This test pins only that TargetIsRunning is callable
	// and does not report a bogus true; the refusal to act on that non-answer
	// belongs to Clean and is pinned by
	// TestAppUninstallerCleanRefusesWhenBundlePathIsUnknown.
	c := &AppUninstaller{target: AppTarget{BundleID: "com.acme.editor"}}

	running, err := c.TargetIsRunning(t.Context())
	if err != nil {
		t.Fatalf("TargetIsRunning() error: %v", err)
	}
	if running {
		t.Error("TargetIsRunning() = true, want false: there is no evidence either way")
	}
}

// Regression: a target resolved only by bundle id (what `uninstall
// com.acme.editor` produces when the .app was never located) used to sail
// straight past the running-process guard, because IsRunning answers
// (false, nil) for an empty BundlePath and Clean read that non-answer as "not
// running". "Cannot verify" must fail closed, exactly like a checker error.
func TestAppUninstallerCleanRefusesWhenBundlePathIsUnknown(t *testing.T) {
	checker := &fakeProcessChecker{running: false}
	c, entries, filePath, dirPath := uninstallerWithFixture(t, checker)
	c.target.BundlePath = ""

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if !result.Skipped {
		t.Fatal("Skipped = false, want true: a missing bundle path makes the running check unanswerable")
	}
	if !strings.Contains(result.SkipReason, "Acme Editor") {
		t.Errorf("SkipReason = %q, want it to name the app", result.SkipReason)
	}
	if !strings.Contains(result.SkipReason, "bundle path is unknown") {
		t.Errorf("SkipReason = %q, want it to explain why the check was impossible", result.SkipReason)
	}
	if result.FilesDeleted != 0 || result.BytesFreed != 0 {
		t.Errorf("FilesDeleted = %d, BytesFreed = %d, want 0 and 0", result.FilesDeleted, result.BytesFreed)
	}
	assertStillOnDisk(t, filePath, dirPath)

	if checker.calls != 0 {
		t.Errorf("IsRunning called %d times, want 0: there was nothing it could answer", checker.calls)
	}
}

// A dry run still previews everything: it deletes nothing by definition, so an
// unverifiable bundle path costs it nothing.
func TestAppUninstallerCleanDryRunUnaffectedByUnknownBundlePath(t *testing.T) {
	c, entries, filePath, dirPath := uninstallerWithFixture(t, &fakeProcessChecker{running: true})
	c.target.BundlePath = ""

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if result.Skipped {
		t.Errorf("dry run Skipped = true (%q), want false", result.SkipReason)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2 previewed", result.FilesDeleted)
	}
	assertStillOnDisk(t, filePath, dirPath)
}

// Defence in depth for the "shared data is never deleted" rule. The bands are
// advice; this makes them binding for a caller that hands Clean an unfiltered
// batch. The shared entry must survive, and the rest of the batch must still go
// through -- one refusal never aborts the batch.
func TestAppUninstallerCleanRefusesSharedEntries(t *testing.T) {
	c, entries, filePath, dirPath := uninstallerWithFixture(t, &fakeProcessChecker{running: false})

	// dirPath stands in for a Group Container shared with another app.
	c.setConfidenceIndex(map[string]Confidence{
		filePath: scoreCandidate([]MatchReason{newMatchReason(matchSourceExactBundleID)}, confidenceFlags{}),
		dirPath:  scoreCandidate([]MatchReason{newMatchReason(matchSourceExactBundleID)}, confidenceFlags{Shared: true}),
	})

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	assertStillOnDisk(t, dirPath)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("the non-shared entry should have been deleted (stat err = %v)", err)
	}

	if result.FilesDeleted != 1 || result.BytesFreed != 1024 {
		t.Errorf("FilesDeleted = %d, BytesFreed = %d, want 1 and 1024", result.FilesDeleted, result.BytesFreed)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %v, want exactly one refusal", result.Errors)
	}
	if !strings.Contains(result.Errors[0].Error(), "shared") {
		t.Errorf("error = %q, want it to say the item is shared", result.Errors[0])
	}
}

// Documented scope of the shared refusal: entries this uninstaller's Scan never
// produced carry no evidence, so Clean has nothing to judge them by and leaves
// them to the caller's own vetting instead of blocking on a guess.
func TestAppUninstallerCleanDeletesUnexplainedEntries(t *testing.T) {
	c, entries, filePath, dirPath := uninstallerWithFixture(t, &fakeProcessChecker{running: false})
	c.setConfidenceIndex(map[string]Confidence{})

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none", result.Errors)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	for _, path := range []string{filePath, dirPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q should have been deleted (stat err = %v)", path, err)
		}
	}
}
