// SPDX-License-Identifier: MIT

package format

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Escape makes text safe to write to a terminal: control characters other than newline
// and tab (C0, DEL and C1, among them ESC and CSI, which start terminal escape
// sequences), and bytes that are not UTF-8, are written as \xNN. Text output carries
// strings a peer chose (a CALLERROR description, a certificate subject, a payload field),
// and these must not move the cursor, rewrite earlier lines or set the window title.
func Escape(s string) string { return escape(s, false) }

// EscapeLine is Escape for a single line or table cell: newline and tab are escaped too,
// so that a value cannot start a line or a column of its own.
func EscapeLine(s string) string { return escape(s, true) }

func unsafeRune(r rune, line bool) bool {
	switch {
	case r == '\n' || r == '\t':
		return line
	case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
		return true
	}
	return false
}

func escape(s string, line bool) string {
	clean := true
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || unsafeRune(r, line) {
			clean = false
			break
		}
		i += size
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			_, _ = fmt.Fprintf(&b, `\x%02x`, s[i])
		case unsafeRune(r, line):
			_, _ = fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
