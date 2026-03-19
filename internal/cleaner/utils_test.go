package cleaner

import "testing"

func TestTotalSize(t *testing.T) {
	tests := []struct {
		name    string
		entries []FileEntry
		want    int64
	}{
		{"nil slice", nil, 0},
		{"empty slice", []FileEntry{}, 0},
		{"single entry", []FileEntry{{Size: 100}}, 100},
		{
			"multiple entries",
			[]FileEntry{{Size: 100}, {Size: 200}, {Size: 300}},
			600,
		},
		{
			"zero size entries",
			[]FileEntry{{Size: 0}, {Size: 0}},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := totalSize(tt.entries); got != tt.want {
				t.Errorf("totalSize() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseDockerSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"0B", 0},
		{"", 0},
		{"  ", 0},
		{"100B", 100},
		{"100kB", 102400},
		{"500MB", 524288000},
		{"1.2GB", 1288490188},
		{"2TB", 2199023255552},
		{"1.5TB", 1649267441664},
		{"0.5GB", 536870912},
		{"10MB", 10485760},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseDockerSize(tt.input); got != tt.want {
				t.Errorf("parseDockerSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDockerSizeInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"garbage", "notanumber"},
		{"no unit", "abc"},
		{"unit only", "GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDockerSize(tt.input); got != 0 {
				t.Errorf("parseDockerSize(%q) = %d, want 0", tt.input, got)
			}
		})
	}
}
