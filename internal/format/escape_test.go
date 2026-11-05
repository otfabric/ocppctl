// SPDX-License-Identifier: MIT

package format

import (
	"bytes"
	"strings"
	"testing"
)

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":           "plain text",
		"two\nlines\tand tab":  "two\nlines\tand tab",
		"\x1b[2Jclear":         `\x1b[2Jclear`,
		"bell\a and \r return": `bell\x07 and \x0d return`,
		"del\x7f":              `del\x7f`,
		"csi \u009b1m":         `csi \x9b1m`,
		"bad utf-8 \x9b\xff":   `bad utf-8 \x9b\xff`,
		"Grüße ✓   nbsp":       "Grüße ✓   nbsp",
		"\x00nul":              `\x00nul`,
		"  line separator":     "  line separator",
	} {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
	if got := EscapeLine("a\tb\nc\x1b"); got != `a\x09b\x0ac\x1b` {
		t.Errorf("EscapeLine: %q", got)
	}
}

// hostile is a result with a string the peer chose.
type hostile struct{ s string }

func (h hostile) MarshalTextOutput() (string, error) {
	return "description: " + h.s + "\nnext line", nil
}
func (h hostile) TableHeaders() []string { return []string{"FIELD", "VALUE"} }
func (h hostile) TableRows() [][]string {
	return [][]string{{"description", h.s}, {"status", "Accepted"}}
}

func rawControl(s string) bool {
	return strings.ContainsAny(s, "\x1b\x07\r") || strings.Contains(s, "\x9b")
}

// Text and table output never carry the terminal control characters of a peer's
// strings; JSON is left as encoding/json writes it.
func TestWriteEscapesPeerStrings(t *testing.T) {
	const evil = "\x1b]0;pwned\x07\x1b[1A\r\u009b2Kok\tforged\nstatus  Rejected"
	for _, f := range []OutputFormat{FormatText, FormatTable} {
		var b bytes.Buffer
		if err := Write(&b, f, hostile{evil}); err != nil {
			t.Fatal(err)
		}
		if rawControl(b.String()) {
			t.Errorf("%s: raw control characters: %q", f, b.String())
		}
		if f == FormatTable && strings.Count(b.String(), "\n") != 4 {
			t.Errorf("table: a cell started a row of its own:\n%s", b.String())
		}
		if f == FormatText && !strings.Contains(b.String(), "\nnext line") {
			t.Errorf("text: the result's own lines are kept: %q", b.String())
		}
	}
	for _, f := range []OutputFormat{FormatText, FormatTable} {
		var b bytes.Buffer
		s := NewStreamWriter(&b, f)
		if err := s.Write(hostile{evil}); err != nil {
			t.Fatal(err)
		}
		if rawControl(b.String()) {
			t.Errorf("stream %s: raw control characters: %q", f, b.String())
		}
	}
}
