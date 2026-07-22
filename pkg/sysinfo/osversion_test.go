package sysinfo

import (
	"context"
	"testing"
)

func TestParseSwVers(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantName    string
		wantVersion string
		wantBuild   string
		ok          bool
	}{
		{
			name:        "full valid output, known major version",
			raw:         "ProductName:\t\tmacOS\nProductVersion:\t\t26.5.2\nBuildVersion:\t\t25F84",
			wantName:    "macOS Tahoe",
			wantVersion: "26.5.2",
			wantBuild:   "25F84",
			ok:          true,
		},
		{
			name:        "missing BuildVersion line",
			raw:         "ProductName:\t\tmacOS\nProductVersion:\t\t15.1",
			wantName:    "macOS Sequoia",
			wantVersion: "15.1",
			wantBuild:   "",
			ok:          true,
		},
		{
			name:        "missing ProductName line",
			raw:         "ProductVersion:\t\t14.2\nBuildVersion:\t\t23C64",
			wantName:    "Sonoma",
			wantVersion: "14.2",
			wantBuild:   "23C64",
			ok:          true,
		},
		{
			name: "empty output",
			raw:  "",
			ok:   false,
		},
		{
			name:        "unknown/future major version falls back gracefully",
			raw:         "ProductName:\t\tmacOS\nProductVersion:\t\t99.0\nBuildVersion:\t\t99A1",
			wantName:    "macOS",
			wantVersion: "99.0",
			wantBuild:   "99A1",
			ok:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseSwVers(tt.raw)
			if ok != tt.ok {
				t.Fatalf("parseSwVers(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.name != tt.wantName || got.version != tt.wantVersion || got.build != tt.wantBuild {
				t.Errorf("parseSwVers(%q) = %+v, want name=%q version=%q build=%q", tt.raw, got, tt.wantName, tt.wantVersion, tt.wantBuild)
			}
		})
	}
}

func TestGatherOSVersionMissingBinary(t *testing.T) {
	orig := swVersPath
	swVersPath = "/nonexistent/sw_vers"
	defer func() { swVersPath = orig }()

	_, ok := gatherOSVersion(context.Background())
	if ok {
		t.Error("expected gatherOSVersion to degrade to unknown when binary is missing")
	}
}
