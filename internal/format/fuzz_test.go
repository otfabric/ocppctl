// SPDX-License-Identifier: MIT

package format

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzEscape checks that escaped text holds no terminal control character and no byte
// that is not UTF-8, that escaping twice changes nothing more, and that safe text is
// left as it is.
func FuzzEscape(f *testing.F) {
	for _, s := range []string{"plain", "a\nb\tc", "\x1b[2J", "\x1b]0;t\x07", "\u009b1m", "\x9b\xff\xfe", "\r", "Grüße", "\x00\x7f"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, line := range []bool{false, true} {
			out := escape(s, line)
			if !utf8.ValidString(out) {
				t.Fatalf("%q escaped to invalid UTF-8 %q", s, out)
			}
			for _, r := range out {
				if unsafeRune(r, line) {
					t.Fatalf("%q escaped to %q, which holds %U", s, out, r)
				}
			}
			if again := escape(out, line); again != out {
				t.Fatalf("escaping twice changed %q to %q", out, again)
			}
			safe := utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return unsafeRune(r, line) })
			if safe && out != s {
				t.Fatalf("safe text %q changed to %q", s, out)
			}
		}
	})
}
