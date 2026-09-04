package elevate

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// maxPlanFileBytes caps how much the root helper will read from the plan file.
// 8 MiB is far more than any realistic plan (a plan is paths and integers;
// ~100k entries would still fit) while keeping a hostile or corrupted file
// from turning the helper into an out-of-memory bomb running as root. The cap
// is enforced twice: once against the stat size, and again through an
// io.LimitReader, since the file could grow between the two.
const maxPlanFileBytes = 8 << 20

// planFilePerm / planDirPerm are the exact permissions the plan file and its
// containing directory must have. Exact, not "at most": 0600/0700 means only
// the owner can read or replace the plan, and anything looser is a way for
// another local account to steer a root process's delete list.
const (
	planFilePerm fs.FileMode = 0o600
	planDirPerm  fs.FileMode = 0o700
)

// readPlanFile opens, validates and decodes the plan file, and is the single
// point where the root helper touches attacker-reachable input.
//
// The validation happens on the *opened file descriptor*, not on the path.
// Doing it on the path (Lstat the name, then open the name) leaves a window in
// which the name can be swapped between the two calls -- the classic
// check-then-use race, which as root is a full compromise. So the file is
// opened once with O_NOFOLLOW (a symlink at the final component fails outright
// rather than being followed somewhere privileged) and every subsequent check
// runs against that same fd via f.Stat(); the name is never resolved twice.
//
// expectedUID is passed in rather than read from the environment here so that
// this function stays a directly testable pure-ish unit; RunHelper is what
// derives it from SUDO_UID.
func readPlanFile(path string, expectedUID int) (Plan, error) {
	// The parent directory can only be checked by name -- there is no fd for
	// it -- but the parent side always creates it with os.MkdirTemp, so a
	// private 0700 directory owned by the invoking user is a property we can
	// require. It closes the "world-writable directory, swap the file"
	// approach; the O_NOFOLLOW open below closes the rest.
	if err := validatePlanDir(filepath.Dir(path), expectedUID); err != nil {
		return Plan{}, err
	}

	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Plan{}, fmt.Errorf("opening plan file: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Plan{}, fmt.Errorf("inspecting plan file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Plan{}, fmt.Errorf("plan file %s is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm != planFilePerm {
		return Plan{}, fmt.Errorf("plan file %s has permissions %04o, want exactly %04o (it must not be readable or writable by anyone but its owner)", path, perm, planFilePerm)
	}
	uid, err := ownerUID(info)
	if err != nil {
		return Plan{}, fmt.Errorf("plan file %s: %w", path, err)
	}
	if uid != expectedUID {
		return Plan{}, fmt.Errorf("plan file %s is owned by uid %d, want uid %d (the user who invoked sudo)", path, uid, expectedUID)
	}
	if info.Size() > maxPlanFileBytes {
		return Plan{}, fmt.Errorf("plan file %s is %d bytes, over the %d byte limit", path, info.Size(), int64(maxPlanFileBytes))
	}

	// Decode from the validated fd, never by re-opening the path. The
	// LimitReader re-applies the cap in case the file grew after Stat.
	var plan Plan
	dec := json.NewDecoder(io.LimitReader(f, maxPlanFileBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("decoding plan file: %w", err)
	}

	return plan, nil
}

// validatePlanDir requires the plan file's parent to be a private directory
// owned by the invoking user, which is exactly what os.MkdirTemp produces on
// the parent side.
func validatePlanDir(dir string, expectedUID int) error {
	// Lstat, not Stat: a symlinked parent directory must be rejected, not
	// silently resolved.
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting plan directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("plan directory %s is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm != planDirPerm {
		return fmt.Errorf("plan directory %s has permissions %04o, want exactly %04o", dir, perm, planDirPerm)
	}
	uid, err := ownerUID(info)
	if err != nil {
		return fmt.Errorf("plan directory %s: %w", dir, err)
	}
	if uid != expectedUID {
		return fmt.Errorf("plan directory %s is owned by uid %d, want uid %d (the user who invoked sudo)", dir, uid, expectedUID)
	}
	return nil
}

// ownerUID extracts the owning uid from a FileInfo. It fails loudly rather
// than defaulting when the platform-specific stat data is unavailable: an
// ownership check that silently degrades to "no check" is worse than none.
func ownerUID(info fs.FileInfo) (int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot determine file ownership on this platform")
	}
	return int(stat.Uid), nil
}
