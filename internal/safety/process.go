// Package safety holds the pre-deletion guards shared by cleaners that remove
// user-visible applications. It is intentionally narrow: it only answers "is
// this application running right now?".
//
// It must not import internal/config (that would create the import cycle
// cleaner -> safety -> config -> cleaner). Protected paths are enforced
// elsewhere in the pipeline, not here.
package safety

import (
	"bufio"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProcessTarget identifies the application whose processes we are looking for.
type ProcessTarget struct {
	BundleID string
	// BundlePath is the on-disk bundle (e.g. /Applications/Foo.app). It is what
	// the match is actually based on: the executable of a running process must
	// live inside <BundlePath>/Contents/MacOS.
	BundlePath string
}

// ProcessChecker reports whether an application is currently running.
type ProcessChecker interface {
	IsRunning(ctx context.Context, target ProcessTarget) (bool, error)
}

// processInfo is one entry of the running process table.
//
// It deliberately carries only the executable path. A pid column used to be
// parsed here, but it was never read by any match, and asking ps for
// `pid=,comm=` is what caused the truncation bug documented on
// listRunningProcesses. If a pid is ever genuinely needed, it must be obtained
// from a separate ps invocation rather than by widening this one.
type processInfo struct {
	execPath string
}

type defaultProcessChecker struct {
	// processLister is injectable so tests never have to shell out.
	processLister func(ctx context.Context) ([]processInfo, error)
}

// NewProcessChecker returns a ProcessChecker backed by the real process table.
func NewProcessChecker() ProcessChecker {
	return &defaultProcessChecker{processLister: listRunningProcesses}
}

// IsRunning reports whether any running process executes a binary that lives
// inside the target bundle's Contents/MacOS directory.
//
// Matching on the executable path (instead of the process name) avoids the
// classic false positive of two vendors shipping similarly named apps.
//
// An empty BundlePath means this checker has nothing to match against, so it
// reports (false, nil). That result means "no evidence", NOT "safe to delete":
// without a bundle path the question is unanswerable. Callers must treat an
// unknown bundle path as "cannot verify" and refuse to delete, rather than
// reading the false as a green light -- see AppUninstaller.Clean, which checks
// for the empty path itself before ever getting here.
//
// A failure to list processes is returned as a real error -- it is NOT the same
// as "not running", and callers are expected to fail closed on it.
func (d *defaultProcessChecker) IsRunning(ctx context.Context, target ProcessTarget) (bool, error) {
	if strings.TrimSpace(target.BundlePath) == "" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	lister := d.processLister
	if lister == nil {
		lister = listRunningProcesses
	}

	processes, err := lister(ctx)
	if err != nil {
		return false, err
	}

	prefix := filepath.Join(filepath.Clean(target.BundlePath), "Contents", "MacOS") + string(filepath.Separator)
	for _, p := range processes {
		if p.execPath == "" {
			continue
		}
		// macOS filesystems are case-insensitive by default, so a
		// case-sensitive comparison could miss a live process.
		if strings.HasPrefix(strings.ToLower(filepath.Clean(p.execPath)), strings.ToLower(prefix)) {
			return true, nil
		}
	}

	return false, nil
}

// listRunningProcesses shells out to ps. On macOS `comm` is the full path of
// the executable, which is exactly what the bundle-prefix match needs.
//
// Exactly one column is requested, and that is load-bearing. Apple's ps clamps
// a multi-column listing to the terminal width (120 columns when there is no
// tty), replacing the tail of the line with "..." -- so `ps -axo pid=,comm=`
// silently mangles every executable path longer than ~114 characters, which is
// routine for a bundle under a long home directory. A truncated path never
// matches the <bundle>/Contents/MacOS/ prefix, so IsRunning would answer
// "not running" for an app that is running: the guard would fail OPEN, which is
// the exact opposite of its contract. With a single `comm=` column ps emits the
// full path. Do not add columns back here.
func listRunningProcesses(ctx context.Context) ([]processInfo, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "comm=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessList(string(out)), nil
}

// parseProcessList reads `ps -axo comm=` output: one executable path per line,
// with no pid column to split off. Paths may contain spaces, so the whole line
// (minus surrounding whitespace) is the path.
func parseProcessList(out string) []processInfo {
	var processes []processInfo

	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		execPath := strings.TrimSpace(scanner.Text())
		if execPath == "" {
			continue
		}
		processes = append(processes, processInfo{execPath: execPath})
	}

	return processes
}
