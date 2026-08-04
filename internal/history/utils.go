package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
)

func historyHomeDir(geteuid func() int, userHomeDir func() (string, error), lookupUser func(string) (*user.User, error), getenv func(string) string) (string, error) {
	// in case the user runs as root because of sudo required for some categories
	if geteuid() == 0 {
		if sudoUser := getenv("SUDO_USER"); sudoUser != "" {
			if sudoProfile, err := lookupUser(sudoUser); err == nil && sudoProfile.HomeDir != "" {
				return sudoProfile.HomeDir, nil
			}
		}
	}
	return userHomeDir()
}

func path() (appDir string, err error) {
	home, err := historyHomeDir(os.Geteuid, os.UserHomeDir, user.Lookup, os.Getenv)
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".tidymymac", "history.json"), nil
}

func loadAtPath(p string) (Record, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, nil
	}
	if err != nil {
		return Record{}, err
	}

	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}

	return record, nil
}

func appendAtPath(p string, run RunRecord) (err error) {
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
		return err
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
		return nil, err
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
