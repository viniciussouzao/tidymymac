package elevate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// HelperCommandName is the hidden cobra subcommand implementing the root side.
// Exported only so cmd/ can declare the command under exactly the name Invoke
// passes to sudo -- a drift between the two would surface as a confusing
// "unknown command" from a process the user just typed a password for.
// It is an internal contract between Invoke and cmd/elevated_clean.go, not a
// public interface.
const HelperCommandName = "internal-elevated-clean"

// planFileFlag is how the plan's location reaches the child. A flag rather
// than stdin because stdin must stay attached to the terminal for sudo's
// password prompt, and a file rather than an argv blob because argv is world
// readable via ps.
const planFileFlag = "--plan-file"

// sudoPrompt replaces sudo's default prompt so the user can see which program
// is asking for their password -- an unexplained "Password:" appearing after a
// keystroke in a TUI is exactly the shape of a credential-phishing prompt.
// %p is expanded by sudo to the account whose password is required.
const sudoPrompt = "[tidymymac] password for %p: "

// sudoPath is the absolute path to sudo. Resolving "sudo" through $PATH would
// let an attacker-controlled PATH entry supply a fake sudo, which is precisely
// the program the custom sudoPrompt above exists to make trustworthy: a fake
// sudo can print the same prompt and harvest the password. /usr/bin/sudo is
// the only sudo on a stock macOS.
const sudoPath = "/usr/bin/sudo"

// HelperGuardRejectedExitCode is the helper's "a guard rejected this run and
// nothing was deleted" channel of the IPC contract.
//
// It exists because an ordinary non-zero exit is ambiguous: it could equally
// mean the helper crashed or was killed *after* it started deleting. The
// helper only ever exits with this code from the pre-deletion guard path, so
// the parent can map it to ErrElevationFailed with confidence, and map every
// other abnormal exit to ErrElevationOutcomeUnknown.
const HelperGuardRejectedExitCode = 3

// ErrElevationFailed means the elevated helper definitely did not delete
// anything: authentication failed or was cancelled, sudo is unavailable, or a
// guard rejected the plan before any deletion could start. The caller may
// report this to the user as "nothing happened".
var ErrElevationFailed = errors.New("elevated helper did not run; nothing was deleted")

// ErrElevationOutcomeUnknown means the helper got past its guards but never
// delivered a readable result: it was interrupted, killed, crashed, or its
// stdout was truncated. Deletion may have already started, so the caller must
// NOT tell the user nothing was deleted -- it must say the outcome is unknown
// and invite a re-scan.
var ErrElevationOutcomeUnknown = errors.New("the elevated helper was interrupted or produced no readable result; a root clean may have partially completed")

// helperKillDelay bounds how long Wait may block after cancellation. Without
// it, os/exec's stdout-copying goroutine keeps Wait blocked until every writer
// closes the pipe -- including a root helper that outlived the sudo process we
// signalled.
const helperKillDelay = 5 * time.Second

// sudoCommand builds the argv for the elevated child. It is a package-level
// var solely so tests can substitute a stub process -- invoking real sudo in a
// test would block on a password prompt. Production code must never reassign
// it; keeping it this small means a test double cannot accidentally change
// anything but which program gets executed.
var sudoCommand = func(ctx context.Context, exePath, planPath string) *exec.Cmd {
	return exec.CommandContext(ctx, sudoPath, "-p", sudoPrompt, exePath, HelperCommandName, planFileFlag, planPath)
}

// sudoAuthCommand builds the argv for the authentication step that precedes
// the helper: "sudo -v" prompts for (or refreshes) the user's credentials and
// runs nothing. It is a separate seam from sudoCommand for the same reason
// that one exists -- tests substitute a stub -- and stays this small for the
// same reason too.
var sudoAuthCommand = func(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, sudoPath, "-p", sudoPrompt, "-v")
}

// Invoke is the unprivileged side of the elevation. It writes plan to a
// private temp file, re-executes this same binary under sudo pointed at that
// file, and decodes the Result the helper prints on stdout.
//
// The child inherits stdin and stderr from this process so sudo's own password
// prompt (and the helper's progress lines) reach the terminal untouched; only
// stdout is captured, because stdout is the result channel and carries nothing
// else. There is deliberately no result *file*: a root process writing to a
// path chosen by an unprivileged caller is a symlink-attack surface, whereas a
// pipe it inherited has no name to attack.
//
// Authentication is a separate step. Invoke first runs "sudo -v", which
// prompts for the password and executes nothing; only once that succeeds does
// it run the helper (which sudo then normally admits without a second prompt,
// on the cached credential). The split exists because sudo's own failure code
// is 1, and 1 is also what the helper -- or the Go runtime, or cobra -- can
// exit with *after* deleting: an "exit 1, empty stdout" observation on a
// single combined invocation cannot distinguish "the password was wrong" from
// "the root clean ran and then failed to report". With authentication proven
// separately, a failure there is provably pre-deletion, and every abnormal
// exit of the helper itself can be treated as the unknown outcome it is.
//
// Error mapping. The parent cannot see what the root child did, so it reports
// only what it can actually prove:
//
//	observation                                   | error
//	----------------------------------------------|---------------------------
//	could not even write the plan                  | ErrElevationFailed
//	sudo -v failed / cancelled / unspawnable       | ErrElevationFailed
//	helper could not be spawned                    | ErrElevationFailed
//	exit HelperGuardRejectedExitCode (3)           | ErrElevationFailed
//	ctx cancelled during the helper run            | ErrElevationOutcomeUnknown
//	any other non-zero exit, signal, or kill       | ErrElevationOutcomeUnknown
//	exit 0 but empty or undecodable stdout         | ErrElevationOutcomeUnknown
//	exit 0, decodable, schema mismatch             | plain error (helper ran)
//	exit 0, decodable Result                       | nil
//
// Only ErrElevationFailed licenses the caller to say "nothing was deleted".
// ErrElevationOutcomeUnknown means the helper was past its guards, so a
// partial clean is possible and the caller must say so. Note that exit 1 is
// deliberately NOT special-cased for the helper: if the cached credential has
// expired between the two steps sudo prompts again, and a failure at that
// second prompt is reported conservatively as unknown rather than risking the
// reverse mistake.
//
// A nil error means the helper ran to completion; the caller must still
// inspect Result.HasErrors for per-category outcomes.
func Invoke(ctx context.Context, plan Plan) (Result, error) {
	if plan.Version == 0 {
		plan.Version = PlanSchemaVersion
	}
	if plan.Version != PlanSchemaVersion {
		return Result{}, fmt.Errorf("plan schema version %d is not supported (this build speaks version %d)", plan.Version, PlanSchemaVersion)
	}
	// Fail before prompting for a password: asking for credentials to do
	// nothing trains users to type their password at prompts that had no
	// reason to appear.
	if !planHasWork(plan) {
		return Result{}, fmt.Errorf("plan contains no entries; nothing to elevate for")
	}

	exePath, err := selfPath()
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrElevationFailed, err)
	}

	dir, planPath, err := writePlanFile(plan)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrElevationFailed, err)
	}
	defer os.RemoveAll(dir)

	if err := authenticate(ctx); err != nil {
		return Result{}, err
	}

	var stdout bytes.Buffer
	cmd := sudoCommand(ctx, exePath, planPath)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = &stdout

	// SIGTERM rather than the default SIGKILL: killing sudo does nothing to the
	// root helper it spawned, which would keep deleting as an orphan. sudo
	// relays SIGTERM to its child, whose signal-aware context (see cmd.Execute)
	// then cancels and stops the cleaners at their ctx.Done() checks.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	// And if the helper ignores that, do not block forever: Wait would
	// otherwise sit on the stdout pipe until the last writer closes it.
	cmd.WaitDelay = helperKillDelay

	runErr := cmd.Run()

	// Cancellation is checked first and always wins: the helper may well have
	// been mid-deletion when we signalled it.
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrElevationOutcomeUnknown, ctx.Err())
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		switch {
		case !errors.As(runErr, &exitErr):
			// The process never started (sudo missing, exec failure): nothing
			// could have run.
			return Result{}, fmt.Errorf("%w: %v", ErrElevationFailed, runErr)
		case exitErr.ExitCode() == HelperGuardRejectedExitCode:
			// The helper's dedicated "guard rejected, nothing deleted" code.
			return Result{}, fmt.Errorf("%w: %v", ErrElevationFailed, runErr)
		default:
			// A signal, a WaitDelay kill, or any other exit code -- including
			// 1, which is both sudo's own failure code and what cobra or the
			// Go runtime exit with on an error after the clean. Authentication
			// was proven separately above, so none of these can be assumed
			// pre-deletion: the helper may have been past its guards and
			// already deleting.
			return Result{}, fmt.Errorf("%w: %v", ErrElevationOutcomeUnknown, runErr)
		}
	}

	// Exit 0 means the helper got past every guard -- its guard path exits 3.
	// So missing or truncated stdout is a failed write *after* the clean, not a
	// clean that never happened.
	if stdout.Len() == 0 {
		return Result{}, fmt.Errorf("%w: helper exited successfully but produced no result", ErrElevationOutcomeUnknown)
	}

	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return Result{}, fmt.Errorf("%w: unreadable result from helper: %v", ErrElevationOutcomeUnknown, err)
	}
	if result.Version != ResultSchemaVersion {
		return Result{}, fmt.Errorf("helper returned result schema version %d, want %d", result.Version, ResultSchemaVersion)
	}

	return result, nil
}

// authenticate runs "sudo -v" with the branded prompt so the user's credential
// is validated (and cached by sudo) before the helper is launched. Nothing is
// executed as root here, so every failure -- a wrong password, a cancelled
// prompt, a policy refusal, a cancelled context, an unspawnable sudo -- is
// provably pre-deletion and maps to ErrElevationFailed.
//
// stdin and stderr are inherited so the prompt reaches the terminal; stdout
// is discarded because "sudo -v" has nothing to say on it and, unlike the
// helper's, is not a result channel.
func authenticate(ctx context.Context) error {
	cmd := sudoAuthCommand(ctx)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = nil

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: cancelled while authenticating: %v", ErrElevationFailed, ctx.Err())
		}
		return fmt.Errorf("%w: sudo authentication failed: %v", ErrElevationFailed, err)
	}
	return nil
}

// selfPath resolves an absolute, symlink-free path to the running binary. The
// symlink resolution matters because the path is about to be handed to sudo:
// what root ends up executing should be the file we are, not whatever a
// (possibly user-writable) symlink points at by the time sudo opens it.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving own executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolving own executable path: %w", err)
	}
	return filepath.Abs(resolved)
}

// writePlanFile creates a private temp directory and writes the plan into it.
// The 0700 directory plus 0600 file are the exact properties readPlanFile
// re-verifies on the root side; the permissions are set explicitly rather than
// relying on the defaults because umask can only ever loosen what we asked for
// here in ways we would rather detect than inherit.
func writePlanFile(plan Plan) (dir string, planPath string, err error) {
	dir, err = os.MkdirTemp("", "tidymymac-elevate-")
	if err != nil {
		return "", "", fmt.Errorf("creating plan directory: %w", err)
	}
	if err := os.Chmod(dir, planDirPerm); err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("securing plan directory: %w", err)
	}

	planPath = filepath.Join(dir, "plan.json")
	f, err := os.OpenFile(planPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, planFilePerm)
	if err != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("creating plan file: %w", err)
	}

	writeErr := json.NewEncoder(f).Encode(plan)
	if chmodErr := f.Chmod(planFilePerm); writeErr == nil {
		writeErr = chmodErr
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing plan file: %w", writeErr)
	}

	return dir, planPath, nil
}

func planHasWork(plan Plan) bool {
	for _, c := range plan.Categories {
		if len(c.Entries) > 0 {
			return true
		}
	}
	return false
}
