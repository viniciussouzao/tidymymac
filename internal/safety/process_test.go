package safety

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func checkerWith(processes []processInfo, err error) *defaultProcessChecker {
	return &defaultProcessChecker{
		processLister: func(context.Context) ([]processInfo, error) {
			return processes, err
		},
	}
}

func TestIsRunning(t *testing.T) {
	running := []processInfo{
		{execPath: "/sbin/launchd"},
		{execPath: "/Applications/Acme Editor.app/Contents/MacOS/Acme Editor"},
		{execPath: "/Applications/Acme Editor Pro.app/Contents/MacOS/Acme Editor"},
		{execPath: "/Applications/Other Vendor/Acme Editor.app/Contents/MacOS/Acme Editor"},
	}

	tests := []struct {
		name   string
		target ProcessTarget
		want   bool
	}{
		{
			name:   "executable inside the bundle matches",
			target: ProcessTarget{BundleID: "com.acme.editor", BundlePath: "/Applications/Acme Editor.app"},
			want:   true,
		},
		{
			name:   "trailing separator in bundle path still matches",
			target: ProcessTarget{BundlePath: "/Applications/Acme Editor.app/"},
			want:   true,
		},
		{
			name: "similar name from a different bundle is not a match",
			// Regression: "different vendor, similar name" must never be a
			// false positive -- /Applications/Sparkle Editor.app is a distinct
			// bundle even though its executable is also called "Acme Editor".
			target: ProcessTarget{BundleID: "com.sparkle.editor", BundlePath: "/Applications/Sparkle Editor.app"},
			want:   false,
		},
		{
			name:   "bundle whose path is a prefix of another bundle is not a match",
			target: ProcessTarget{BundlePath: "/Applications/Acme.app"},
			want:   false,
		},
		{
			name:   "not running at all",
			target: ProcessTarget{BundleID: "com.acme.ghost", BundlePath: "/Applications/Ghost.app"},
			want:   false,
		},
		{
			// "no evidence", not "safe to delete": with no bundle path there
			// is nothing to match executables against. Refusing to act on this
			// non-answer is the caller's job -- see
			// TestAppUninstallerCleanRefusesWhenBundlePathIsUnknown.
			name:   "empty bundle path yields no evidence",
			target: ProcessTarget{BundleID: "com.acme.editor"},
			want:   false,
		},
		{
			name:   "case-insensitive match (macOS default filesystem)",
			target: ProcessTarget{BundlePath: "/applications/acme editor.app"},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := checkerWith(running, nil)

			got, err := checker.IsRunning(t.Context(), tt.target)
			if err != nil {
				t.Fatalf("IsRunning() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRunningEmptyBundlePathDoesNotListProcesses(t *testing.T) {
	called := false
	checker := &defaultProcessChecker{
		processLister: func(context.Context) ([]processInfo, error) {
			called = true
			return nil, errors.New("must not be called")
		},
	}

	got, err := checker.IsRunning(t.Context(), ProcessTarget{})
	if err != nil {
		t.Fatalf("IsRunning() error: %v", err)
	}
	if got {
		t.Error("IsRunning() = true, want false for an empty bundle path")
	}
	if called {
		t.Error("process lister must not run when there is nothing to check")
	}
}

func TestIsRunningPropagatesListerError(t *testing.T) {
	wantErr := errors.New("ps failed")
	checker := checkerWith(nil, wantErr)

	got, err := checker.IsRunning(t.Context(), ProcessTarget{BundlePath: "/Applications/Acme Editor.app"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("IsRunning() error = %v, want %v", err, wantErr)
	}
	if got {
		t.Error("IsRunning() = true, want false alongside an error")
	}
}

func TestIsRunningContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	checker := checkerWith([]processInfo{{execPath: "/sbin/launchd"}}, nil)
	if _, err := checker.IsRunning(ctx, ProcessTarget{BundlePath: "/Applications/Acme Editor.app"}); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestNewProcessCheckerUsesRealLister(t *testing.T) {
	checker, ok := NewProcessChecker().(*defaultProcessChecker)
	if !ok {
		t.Fatal("NewProcessChecker() did not return *defaultProcessChecker")
	}
	if checker.processLister == nil {
		t.Fatal("NewProcessChecker() must wire the real process lister")
	}
}

func TestParseProcessList(t *testing.T) {
	out := "/sbin/launchd\n" +
		"/Applications/Acme Editor.app/Contents/MacOS/Acme Editor\n" +
		"\n" +
		"   \n" +
		"  /usr/libexec/indented \n"

	got := parseProcessList(out)
	if len(got) != 3 {
		t.Fatalf("parsed %d processes, want 3: %+v", len(got), got)
	}
	want := []string{
		"/sbin/launchd",
		"/Applications/Acme Editor.app/Contents/MacOS/Acme Editor",
		"/usr/libexec/indented",
	}
	for i, w := range want {
		if got[i].execPath != w {
			t.Errorf("got[%d].execPath = %q, want %q", i, got[i].execPath, w)
		}
	}
}

// A realistic bundle for an app installed under a long home directory. What
// matters is that the match prefix -- <bundle>/Contents/MacOS/, 118 chars here
// -- is itself past the 120-column line clamp Apple's ps applies to a
// multi-column listing, because that is precisely when truncation destroys the
// match rather than merely shortening the executable name.
const (
	longBundlePath     = "/Users/firstname.lastname/Applications/Vendor Suite Collection Pro/Some Very Long Application Name.app"
	longBundleExecPath = longBundlePath + "/Contents/MacOS/Some Very Long Application Name"
)

// Regression for the `ps -axo pid=,comm=` truncation bug. Asking ps for a pid
// column alongside comm made it clamp every line to 120 columns and replace the
// tail with "...", so a long bundle path never matched the
// <bundle>/Contents/MacOS/ prefix and IsRunning reported "not running" for a
// running app -- the guard failed OPEN.
func TestParseProcessListKeepsLongPathsIntact(t *testing.T) {
	if len(longBundleExecPath) <= 120 {
		t.Fatalf("fixture is only %d chars; it must exceed the 120-column clamp to be a regression", len(longBundleExecPath))
	}

	got := parseProcessList(longBundleExecPath + "\n")
	if len(got) != 1 {
		t.Fatalf("parsed %d processes, want 1", len(got))
	}
	if got[0].execPath != longBundleExecPath {
		t.Fatalf("execPath = %q, want the full untruncated path %q", got[0].execPath, longBundleExecPath)
	}
}

// The end-to-end half of the same regression: a full-length line coming out of
// the lister must make IsRunning say "running". If anyone reintroduces a second
// ps column, the real lister starts emitting the truncated variant this test
// also pins as a non-match, and this assertion is what breaks.
func TestIsRunningMatchesLongBundlePath(t *testing.T) {
	const bundlePath = longBundlePath

	t.Run("full path from a single-column listing matches", func(t *testing.T) {
		checker := checkerWith(parseProcessList(longBundleExecPath+"\n"), nil)

		got, err := checker.IsRunning(t.Context(), ProcessTarget{BundlePath: bundlePath})
		if err != nil {
			t.Fatalf("IsRunning() error: %v", err)
		}
		if !got {
			t.Fatal("IsRunning() = false for a running app with a long bundle path; the guard failed open")
		}
	})

	t.Run("truncated path from a multi-column listing would not match", func(t *testing.T) {
		// Exactly what `ps -axo pid=,comm=` used to hand us: 120 columns with
		// the tail replaced by "...". Documented here so the cost of adding a
		// column back to the ps invocation stays visible.
		truncated := longBundleExecPath[:117] + "..."
		checker := checkerWith(parseProcessList(truncated+"\n"), nil)

		got, err := checker.IsRunning(t.Context(), ProcessTarget{BundlePath: bundlePath})
		if err != nil {
			t.Fatalf("IsRunning() error: %v", err)
		}
		if got {
			t.Fatal("a truncated ps line unexpectedly matched; the fixture no longer models the bug")
		}
	})
}

// TestListRunningProcessesIsNotTruncated exercises the real ps on the real
// machine: whatever the longest executable path currently running is, it must
// not come back with ps's truncation marker.
func TestListRunningProcessesIsNotTruncated(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ps column semantics are macOS-specific")
	}

	processes, err := listRunningProcesses(t.Context())
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	if len(processes) == 0 {
		t.Skip("ps returned no processes")
	}

	longest := 0
	for _, p := range processes {
		if strings.HasSuffix(p.execPath, "...") {
			t.Errorf("ps returned a truncated executable path (%d chars): %q", len(p.execPath), p.execPath)
		}
		if len(p.execPath) > longest {
			longest = len(p.execPath)
		}
	}
	t.Logf("longest executable path seen: %d chars", longest)
}
