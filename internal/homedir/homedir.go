// Package homedir resolves the current user's home directory, accounting for
// the case where the process is running elevated via sudo.
package homedir

import (
	"os"
	"os/user"
	"path/filepath"
)

func resolve(geteuid func() int, userHomeDir func() (string, error), lookupUser func(string) (*user.User, error), getenv func(string) string) (string, error) {
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

// Resolve returns the current user's home directory. When running elevated
// via sudo, it returns the real (SUDO_USER) user's home directory instead of
// root's, so app data is written where the user expects it.
func Resolve() (string, error) {
	return resolve(os.Geteuid, os.UserHomeDir, user.Lookup, os.Getenv)
}

// AppDir returns the tidymymac app data directory (~/.tidymymac), without
// creating it.
func AppDir() (string, error) {
	home, err := Resolve()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tidymymac"), nil
}

func isElevatedWithoutSudoUser(geteuid func() int, lookupUser func(string) (*user.User, error), getenv func(string) string) bool {
	if geteuid() != 0 {
		return false
	}
	sudoUser := getenv("SUDO_USER")
	if sudoUser == "" {
		return true
	}
	profile, err := lookupUser(sudoUser)
	return err != nil || profile.HomeDir == ""
}

// IsElevatedWithoutSudoUser reports whether the process is running elevated
// (euid 0) but SUDO_USER cannot be resolved to a real user's home directory.
// In that case Resolve falls back to root's own home (typically /var/root),
// which is almost never what a caller managing per-user app data actually
// wants -- callers for whom that fallback would be a silent correctness or
// safety problem (e.g. loading a safety config) should treat this as fatal
// rather than proceed against the wrong home directory.
func IsElevatedWithoutSudoUser() bool {
	return isElevatedWithoutSudoUser(os.Geteuid, user.Lookup, os.Getenv)
}
