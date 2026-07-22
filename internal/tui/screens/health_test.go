package screens

import (
	"testing"

	"github.com/viniciussouzao/tidymymac/pkg/sysinfo"
)

func TestFormatOSInfo(t *testing.T) {
	tests := []struct {
		name string
		info sysinfo.Info
		want string
	}{
		{
			name: "complete information",
			info: sysinfo.Info{OSName: "macOS Sequoia", OSVersion: "15.1", OSBuild: "24B83"},
			want: "macOS Sequoia 15.1 (24B83)",
		},
		{
			name: "missing build",
			info: sysinfo.Info{OSName: "macOS Sequoia", OSVersion: "15.1"},
			want: "macOS Sequoia 15.1",
		},
		{
			name: "missing name",
			info: sysinfo.Info{OSVersion: "15.1", OSBuild: "24B83"},
			want: "15.1 (24B83)",
		},
		{
			name: "name only",
			info: sysinfo.Info{OSName: "macOS"},
			want: "macOS",
		},
		{
			name: "build only",
			info: sysinfo.Info{OSBuild: "24B83"},
			want: "(24B83)",
		},
		{
			name: "no information",
			info: sysinfo.Info{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatOSInfo(tt.info); got != tt.want {
				t.Errorf("formatOSInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
