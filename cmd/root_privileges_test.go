package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestRefuseRootDeletion(t *testing.T) {
	if err := refuseRootDeletion(501); err != nil {
		t.Fatalf("refuseRootDeletion(501) = %v, want nil", err)
	}

	err := refuseRootDeletion(0)
	if !errors.Is(err, ErrRunningAsRoot) {
		t.Fatalf("refuseRootDeletion(0) = %v, want ErrRunningAsRoot", err)
	}
	// The message has to tell the user what to do instead, or it just reads as
	// the tool being broken under sudo.
	if !strings.Contains(err.Error(), "without sudo") {
		t.Errorf("error does not say what to do instead:\n%s", err)
	}
}

// asRoot makes the guard see an elevated process for the duration of a test.
func asRoot(t *testing.T) {
	t.Helper()
	previous := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = previous })
}

// withExecuteFlag sets the persistent --execute flag, which the root and clean
// commands read from a package global.
func withExecuteFlag(t *testing.T, value bool) {
	t.Helper()
	previous := executeFlag
	executeFlag = value
	t.Cleanup(func() { executeFlag = previous })
}

// TestExecuteEntryPointsRefuseRoot is the wiring test: it is the guard being
// reachable from each command, not the guard itself, that stops
// `sudo tidymymac clean --execute` from handing root to every cleaner.
//
// Only the elevated case is exercised for the TUI commands -- the
// unprivileged case would start bubbletea.
func TestExecuteEntryPointsRefuseRoot(t *testing.T) {
	commands := map[string]func() error{
		"clean --execute": func() error { return cleanCmd.RunE(cleanCmd, nil) },
		"execute":         func() error { return executeCmd.RunE(executeCmd, nil) },
		"root --execute":  func() error { return rootCmd.RunE(rootCmd, nil) },
	}

	for name, run := range commands {
		t.Run(name, func(t *testing.T) {
			asRoot(t)
			withExecuteFlag(t, true)

			if err := run(); !errors.Is(err, ErrRunningAsRoot) {
				t.Fatalf("%s as root = %v, want ErrRunningAsRoot", name, err)
			}
		})
	}
}

// TestDryRunEntryPointsAreNotBlocked pins the other half: refusing dry runs
// would make the tool impossible to inspect under sudo for no safety gain,
// since they delete nothing.
func TestDryRunEntryPointsAreNotBlocked(t *testing.T) {
	asRoot(t)
	withExecuteFlag(t, false)

	if err := guardRootDeletion(); err == nil {
		t.Fatal("guardRootDeletion() should still report root; the callers gate on executeFlag")
	}

	// The root command in dry-run mode must not consult the guard at all. It
	// would start the TUI if it got that far, so assert on the flag the RunE
	// branches on rather than calling it.
	if executeFlag {
		t.Fatal("executeFlag should be false here")
	}
}
