package utils

import "testing"

func TestSanitizeForTerminal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii path untouched", "/Users/vini/Library/Caches/x.tmp", "/Users/vini/Library/Caches/x.tmp"},
		{"unicode file name untouched", "/tmp/relatório final – ção.txt", "/tmp/relatório final – ção.txt"},
		{"emoji untouched", "/tmp/📦 build", "/tmp/📦 build"},
		{"newline cannot inject a row", "/tmp/a\n   1.0 GB  /etc/fake", `/tmp/a\n   1.0 GB  /etc/fake`},
		{"carriage return cannot overwrite the line", "/tmp/a\rHIDDEN", `/tmp/a\rHIDDEN`},
		{"tab is made visible", "/tmp/a\tb", `/tmp/a\tb`},
		{"ANSI escape cannot reach the terminal", "/tmp/\x1b[31mred\x1b[0m", `/tmp/\e[31mred\e[0m`},
		{"OSC title sequence", "\x1b]0;pwned\x07", `\e]0;pwned\x07`},
		{"other C0 control", "/tmp/a\x00b", `/tmp/a\x00b`},
		{"DEL", "/tmp/a\x7fb", `/tmp/a\x7fb`},
		{"C1 control", "/tmp/a\u0085b", `/tmp/a\u{85}b`},
		{"unicode line separator", "/tmp/a\u2028b", `/tmp/a\u{2028}b`},
		{"invalid utf-8", "/tmp/a\xffb", `/tmp/a\u{fffd}b`},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeForTerminal(tt.in); got != tt.want {
				t.Fatalf("SanitizeForTerminal(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
