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
	"strconv"
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
type processInfo struct {
	pid      int
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
// An empty BundlePath means there is nothing to check: (false, nil).
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
func listRunningProcesses(ctx context.Context) ([]processInfo, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	return parseProcessList(string(out)), nil
}

// parseProcessList reads `ps -axo pid=,comm=` output. Executable paths may
// contain spaces, so only the first field is treated as the pid.
func parseProcessList(out string) []processInfo {
	var processes []processInfo

	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimLeft(scanner.Text(), " \t")
		if line == "" {
			continue
		}

		pidField, rest, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		pid, err := strconv.Atoi(pidField)
		if err != nil {
			continue
		}

		execPath := strings.TrimSpace(rest)
		if execPath == "" {
			continue
		}
		processes = append(processes, processInfo{pid: pid, execPath: execPath})
	}

	return processes
}
