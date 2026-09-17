package safety

import (
	"context"
	"errors"
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
		{pid: 1, execPath: "/sbin/launchd"},
		{pid: 42, execPath: "/Applications/Acme Editor.app/Contents/MacOS/Acme Editor"},
		{pid: 43, execPath: "/Applications/Acme Editor Pro.app/Contents/MacOS/Acme Editor"},
		{pid: 44, execPath: "/Applications/Other Vendor/Acme Editor.app/Contents/MacOS/Acme Editor"},
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
			name:   "empty bundle path means nothing to check",
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

	checker := checkerWith([]processInfo{{pid: 1, execPath: "/sbin/launchd"}}, nil)
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
	out := "    1 /sbin/launchd\n" +
		"  431 /Applications/Acme Editor.app/Contents/MacOS/Acme Editor\n" +
		"\n" +
		"notapid /bin/bogus\n" +
		"  999\n" +
		"  777 \n"

	got := parseProcessList(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d processes, want 2: %+v", len(got), got)
	}
	if got[0].pid != 1 || got[0].execPath != "/sbin/launchd" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].pid != 431 || got[1].execPath != "/Applications/Acme Editor.app/Contents/MacOS/Acme Editor" {
		t.Errorf("got[1] = %+v (executable paths with spaces must be preserved)", got[1])
	}
}
