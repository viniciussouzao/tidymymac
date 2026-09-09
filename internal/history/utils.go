package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/viniciussouzao/tidymymac/internal/homedir"
)

// geteuid is indirected so tests can exercise appendAtPath's root guard
// without actually being root, mirroring cmd/root_privileges.go's own seam
// for the same check.
var geteuid = os.Geteuid

func path() (appDir string, err error) {
	dir, err := homedir.AppDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "history.json"), nil
}

func loadAtPath(p string) (Record, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, withOwnershipHint(p, err)
	}

	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}

	return record, nil
}

// withOwnershipHint annotates a permission-denied error on an app-dir file
// with the most likely cause and its fix. The only way p ends up owned by
// another user is a past run that reached this code while elevated -- Append
// now refuses that outright (see the geteuid check below), but a file
// created by an older build, or by a command that bypassed that guard,
// leaves the file stuck exactly this way: unreadable to the real user
// forever after, with a bare "permission denied" giving no hint why. Other
// errors are returned unchanged since they aren't this specific, diagnosable
// case.
func withOwnershipHint(p string, err error) error {
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	return fmt.Errorf("%w (likely created by a previous run under sudo and now owned by another user; fix without losing history with: sudo chown $(id -un):$(id -gn) %s %s)", err, filepath.Dir(p), p)
}

func appendAtPath(p string, run RunRecord) (err error) {
	if geteuid() == 0 {
		return fmt.Errorf("refusing to write %s while running as root: every legitimate caller writes history unprivileged, so this would leave the file owned by root and unreadable to the real user afterwards", p)
	}

	lockFile, err := lockHistoryFile(p)
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := unlockHistoryFile(lockFile); unlockErr != nil && err == nil {
			err = unlockErr
		}
	}()

	record, err := loadAtPath(p)
	if err != nil {
		return err
	}

	run.ID = len(record.Runs) + 1
	record.Runs = append(record.Runs, run)

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(p), "history-*.json")
	if err != nil {
		return withOwnershipHint(filepath.Dir(p), err)
	}
	defer func() {
		if removeErr := os.Remove(tmp.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = fmt.Errorf("remove temporary history file: %w", removeErr)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(record); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), p)
}

func lockHistoryFile(p string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}

	lockPath := p + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, withOwnershipHint(lockPath, err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, err
	}

	return lockFile, nil
}

func unlockHistoryFile(lockFile *os.File) error {
	if lockFile == nil {
		return nil
	}

	var errs []error
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		errs = append(errs, fmt.Errorf("unlock history file: %w", err))
	}
	if err := lockFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close history lock file: %w", err))
	}
	if err := os.Remove(lockFile.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove history lock file: %w", err))
	}

	return errors.Join(errs...)
}
