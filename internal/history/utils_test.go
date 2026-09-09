package history

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// asRoot makes appendAtPath's guard see an elevated process for the duration
// of a test, mirroring cmd/root_privileges_test.go's own seam for the same
// kind of check.
func asRoot(t *testing.T) {
	t.Helper()
	previous := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = previous })
}

// TestAppendRefusesRoot pins the fix for the bug where running a clean with a
// sudo-requiring category left history.json owned by root: every legitimate
// caller writes history unprivileged, so appendAtPath must refuse outright
// when it isn't, rather than silently creating a file the real user can
// never read again.
func TestAppendRefusesRoot(t *testing.T) {
	asRoot(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")

	err := appendAtPath(p, RunRecord{})
	if err == nil {
		t.Fatal("appendAtPath as root = nil, want an error")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error does not explain the refusal is about running as root:\n%s", err)
	}
	if _, statErr := os.Stat(p); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("appendAtPath as root must not create %s, but it exists (stat err: %v)", p, statErr)
	}
}

// TestLoadUnreadableFileHintsAtOwnership pins the other half: a history.json
// that already exists but can't be read (the exact state a pre-fix root run
// left behind) must fail with a message that tells the user why and how to
// fix it, not a bare "permission denied".
func TestLoadUnreadableFileHintsAtOwnership(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file regardless of mode; this test needs to run unprivileged")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")
	if err := os.WriteFile(p, []byte(`{"runs":[]}`), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("chmod fixture file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	_, err := loadAtPath(p)
	if err == nil {
		t.Fatal("loadAtPath on an unreadable file = nil, want an error")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error does not hint at the sudo/ownership fix:\n%s", err)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")

	record, err := loadAtPath(p)
	if err != nil {
		t.Fatalf("loadAtPath on a missing file = %v, want nil", err)
	}
	if len(record.Runs) != 0 {
		t.Fatalf("loadAtPath on a missing file = %+v, want an empty record", record)
	}
}

// TestAppendRoundTrip is a basic sanity check that the unprivileged path
// still works after the root guard was added.
func TestAppendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "history.json")

	if err := appendAtPath(p, RunRecord{TotalFiles: 3, TotalBytes: 100}); err != nil {
		t.Fatalf("appendAtPath: %v", err)
	}

	record, err := loadAtPath(p)
	if err != nil {
		t.Fatalf("loadAtPath: %v", err)
	}
	if len(record.Runs) != 1 || record.Runs[0].TotalFiles != 3 {
		t.Fatalf("loadAtPath after append = %+v, want one run with TotalFiles=3", record)
	}
}
