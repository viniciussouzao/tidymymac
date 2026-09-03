package utils

import (
	"fmt"
	"strings"
	"unicode"
)

// SanitizeForTerminal returns s with every control character replaced by a
// visible escape, so untrusted text (file names, Docker image tags) can be
// written to a terminal without being able to inject rows, move the cursor,
// retitle the window or otherwise make the rendered report differ from what
// the program emitted. Ordinary printable Unicode is left untouched.
//
// It is display-only: never feed the result back to anything that acts on
// paths.
func SanitizeForTerminal(s string) string {
	// Fast path: nothing to escape.
	clean := true
	for _, r := range s {
		if !isTerminalSafe(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case isTerminalSafe(r):
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == 0x1b:
			b.WriteString(`\e`)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u{%x}`, r)
		}
	}
	return b.String()
}

// isTerminalSafe reports whether r can be written to a terminal verbatim.
// Space is allowed; every other non-printing rune (C0/C1 controls, DEL, the
// Unicode line/paragraph separators, unassigned code points, U+FFFD from
// invalid UTF-8) is not.
func isTerminalSafe(r rune) bool {
	if r == ' ' {
		return true
	}
	if r == unicode.ReplacementChar {
		return false
	}
	return unicode.IsPrint(r)
}
