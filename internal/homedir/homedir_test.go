package homedir

import (
	"errors"
	"os/user"
	"testing"
)

func TestResolve_NotElevatedUsesUserHomeDir(t *testing.T) {
	home, err := resolve(
		func() int { return 501 },
		func() (string, error) { return "/Users/regular", nil },
		func(string) (*user.User, error) {
			t.Fatal("lookupUser should not be called when not elevated")
			return nil, nil
		},
		func(string) string { t.Fatal("getenv should not be called when not elevated"); return "" },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if home != "/Users/regular" {
		t.Errorf("got %q, want /Users/regular", home)
	}
}

func TestResolve_ElevatedWithSudoUserReturnsRealUserHome(t *testing.T) {
	home, err := resolve(
		func() int { return 0 },
		func() (string, error) { return "/var/root", nil },
		func(username string) (*user.User, error) {
			if username != "vini" {
				t.Errorf("looked up %q, want vini", username)
			}
			return &user.User{HomeDir: "/Users/vini"}, nil
		},
		func(key string) string {
			if key == "SUDO_USER" {
				return "vini"
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if home != "/Users/vini" {
		t.Errorf("got %q, want /Users/vini", home)
	}
}

func TestResolve_ElevatedWithoutSudoUserFallsBackToUserHomeDir(t *testing.T) {
	home, err := resolve(
		func() int { return 0 },
		func() (string, error) { return "/var/root", nil },
		func(string) (*user.User, error) {
			t.Fatal("lookupUser should not be called without SUDO_USER")
			return nil, nil
		},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if home != "/var/root" {
		t.Errorf("got %q, want /var/root", home)
	}
}

func TestResolve_ElevatedWithLookupErrorFallsBackToUserHomeDir(t *testing.T) {
	home, err := resolve(
		func() int { return 0 },
		func() (string, error) { return "/var/root", nil },
		func(string) (*user.User, error) { return nil, errors.New("no such user") },
		func(key string) string {
			if key == "SUDO_USER" {
				return "ghost"
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if home != "/var/root" {
		t.Errorf("got %q, want /var/root", home)
	}
}

func TestIsElevatedWithoutSudoUser_NotElevated(t *testing.T) {
	got := isElevatedWithoutSudoUser(
		func() int { return 501 },
		func(string) (*user.User, error) { t.Fatal("lookupUser should not be called when not elevated"); return nil, nil },
		func(string) string { t.Fatal("getenv should not be called when not elevated"); return "" },
	)
	if got {
		t.Error("got true, want false when not elevated")
	}
}

func TestIsElevatedWithoutSudoUser_ElevatedWithValidSudoUser(t *testing.T) {
	got := isElevatedWithoutSudoUser(
		func() int { return 0 },
		func(string) (*user.User, error) { return &user.User{HomeDir: "/Users/vini"}, nil },
		func(key string) string {
			if key == "SUDO_USER" {
				return "vini"
			}
			return ""
		},
	)
	if got {
		t.Error("got true, want false when SUDO_USER resolves to a real user")
	}
}

func TestIsElevatedWithoutSudoUser_ElevatedWithMissingSudoUser(t *testing.T) {
	got := isElevatedWithoutSudoUser(
		func() int { return 0 },
		func(string) (*user.User, error) { t.Fatal("lookupUser should not be called without SUDO_USER"); return nil, nil },
		func(string) string { return "" },
	)
	if !got {
		t.Error("got false, want true when elevated with no SUDO_USER set")
	}
}

func TestIsElevatedWithoutSudoUser_ElevatedWithUnresolvableSudoUser(t *testing.T) {
	got := isElevatedWithoutSudoUser(
		func() int { return 0 },
		func(string) (*user.User, error) { return nil, errors.New("no such user") },
		func(key string) string {
			if key == "SUDO_USER" {
				return "ghost"
			}
			return ""
		},
	)
	if !got {
		t.Error("got false, want true when SUDO_USER doesn't resolve to a real user")
	}
}

func TestResolve_PropagatesUserHomeDirError(t *testing.T) {
	wantErr := errors.New("no home dir")
	_, err := resolve(
		func() int { return 501 },
		func() (string, error) { return "", wantErr },
		func(string) (*user.User, error) { return nil, nil },
		func(string) string { return "" },
	)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}
