// SPDX-License-Identifier: MIT

package csms

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/errs"
)

func TestParsePasswords(t *testing.T) {
	// A file saved with a byte order mark: the first station is CS1, not "\uFEFFCS1",
	// which no station could connect as.
	got, err := parsePasswords(strings.NewReader("\uFEFFCS1:one\r\n# comment\n  \n CS2 :two:three\n"), "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["CS1"] != "one" || got["CS2"] != "two:three" {
		t.Errorf("got %q", got)
	}
	// A byte order mark elsewhere is not removed.
	if got, err := parsePasswords(strings.NewReader("CS1:one\n\uFEFFCS2:two\n"), "pw"); err != nil || got["\uFEFFCS2"] != "two" {
		t.Errorf("got %q %v", got, err)
	}

	for name, tc := range map[string]struct {
		in   string
		code string
		line string
	}{
		"no colon":      {"CS1:a\nsecret-without-colon\n", errs.CodeInputDecodeFailed, "line 2"},
		"no password":   {"CS1:\n", errs.CodeInputDecodeFailed, "line 1"},
		"no identity":   {" :secret-x\n", errs.CodeInputDecodeFailed, "line 1"},
		"line too long": {"CS1:a\nCS2:" + strings.Repeat("s", maxPasswordLine) + "\n", errs.CodeInputDecodeFailed, "line 2"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parsePasswords(strings.NewReader(tc.in), "pw")
			e, ok := errs.As(err)
			if !ok || e.Code != tc.code || !strings.Contains(e.Message, tc.line) {
				t.Fatalf("got %v, want %s at %s", err, tc.code, tc.line)
			}
			if strings.Contains(e.Message, "secret") || strings.Contains(e.Message, "sss") {
				t.Errorf("a password was echoed: %s", e.Message)
			}
		})
	}
}

// FuzzParsePasswords feeds passwords files: a file is refused without echoing any of its
// lines, or read into identities that are trimmed and not empty, with passwords that are
// not empty.
func FuzzParsePasswords(f *testing.F) {
	refusal := regexp.MustCompile(`^--passwords-file pw line [0-9]+: (want identity:password|longer than [0-9]+ bytes)$`)
	for _, s := range []string{
		"",
		"CS1:secret\n",
		"\uFEFFCS1:secret\r\nCS2:x:y\n",
		"# comment\n\n  CS3 : spaced \n",
		"nocolon\n",
		":nopassword\n",
		"CS1:\n",
		"CS1:a\nCS1:b\n",
		"\x00:\xff\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := parsePasswords(bytes.NewReader(data), "pw")
		if err != nil {
			e, ok := errs.As(err)
			if !ok {
				t.Fatalf("untyped error %v", err)
			}
			if !refusal.MatchString(e.Message) {
				t.Fatalf("the error says more than where the file is wrong: %s", e.Message)
			}
			return
		}
		for id, pw := range got {
			if id == "" || id != strings.TrimSpace(id) || pw == "" {
				t.Fatalf("entry %q:%q", id, pw)
			}
		}
	})
}
