package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestDefaultValues(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Version", Version, "dev"},
		{"Commit", Commit, "none"},
		{"BuildDate", BuildDate, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestPlatform(t *testing.T) {
	got := Platform()
	want := runtime.GOOS + "/" + runtime.GOARCH

	if got != want {
		t.Errorf("Platform() = %q, want %q", got, want)
	}

	if !strings.Contains(got, "/") {
		t.Errorf("Platform() = %q, expected format GOOS/GOARCH", got)
	}
}

func TestGoVersion(t *testing.T) {
	got := GoVersion()
	want := runtime.Version()

	if got != want {
		t.Errorf("GoVersion() = %q, want %q", got, want)
	}

	if !strings.HasPrefix(got, "go") {
		t.Errorf("GoVersion() = %q, expected prefix \"go\"", got)
	}
}
