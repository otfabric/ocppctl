// SPDX-License-Identifier: MIT

package conn

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/readlimit"
)

// object is a JSON object of exactly n bytes (n >= 8).
func object(n int) string { return `{"a":"` + strings.Repeat("x", n-8) + `"}` }

// endless is a stdin that never ends.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// A payload from a file or stdin is at most readlimit.Payload bytes: one more is
// invalid_payload (exit 3), and nothing past the limit is read.
func TestReadPayloadLimit(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{8, readlimit.Payload - 1, readlimit.Payload, readlimit.Payload + 1, 1 << 20} {
		body := object(n)
		f := filepath.Join(dir, "p.json")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, src := range []struct {
			arg   string
			stdin string
		}{{"-", body}, {"@" + f, ""}} {
			got, err := ReadPayload(src.arg, strings.NewReader(src.stdin))
			if n <= readlimit.Payload {
				if err != nil || len(got) != n {
					t.Errorf("%s, %d bytes: %d bytes, %v", src.arg[:1], n, len(got), err)
				}
				continue
			}
			e, ok := errs.As(err)
			if !ok || e.Kind != errs.KindInvalidInput || e.Code != errs.CodeInvalidPayload || !strings.Contains(e.Message, "68 KiB") {
				t.Errorf("%s, %d bytes: %v, want invalid_payload", src.arg[:1], n, err)
			}
		}
	}
}

// An endless stdin is refused once the limit has passed, within an allocation budget.
func TestReadPayloadEndlessStdin(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := ReadPayload("-", endless{})
	runtime.ReadMemStats(&after)
	if e, ok := errs.As(err); !ok || e.Code != errs.CodeInvalidPayload {
		t.Fatalf("%v, want invalid_payload", err)
	}
	// io.ReadAll grows its buffer by doubling: a few times the limit at most.
	if alloc, budget := after.TotalAlloc-before.TotalAlloc, uint64(8*readlimit.Payload); alloc > budget {
		t.Errorf("allocated %d bytes, budget %d", alloc, budget)
	}
}

// --password-file is at most readlimit.Secret bytes; a larger file is refused without its
// content in the error.
func TestPasswordFileLimit(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1, readlimit.Secret - 1, readlimit.Secret, readlimit.Secret + 1} {
		pw := filepath.Join(dir, "pw")
		if err := os.WriteFile(pw, []byte(strings.Repeat("s", n)), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := Target{URL: "ws://h/CS1", Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s",
			InsecureTransport: true, PasswordFile: pw}.Parse()
		if n <= readlimit.Secret {
			if err != nil || len(p.Password) != n {
				t.Errorf("%d bytes: %v", n, err)
			}
			continue
		}
		e, ok := errs.As(err)
		if !ok || e.Kind != errs.KindInvalidInput || e.Code != errs.CodeInputDecodeFailed || strings.Contains(errText(err), "sss") {
			t.Errorf("%d bytes: %v, want input_decode_failed without the content", n, err)
		}
	}
}

// --header-file holds NAME=VALUE lines; blank lines, # comments, CRLF line ends and a
// byte order mark are allowed, and its headers come before those of --header.
func TestHeaderFile(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		f := filepath.Join(dir, "h")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	parse := func(file string, flags ...string) (*Parsed, error) {
		return Target{URL: "ws://h/CS1", Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s",
			InsecureTransport: true, HeaderFile: file, Headers: flags}.Parse()
	}
	p, err := parse(write("\uFEFFX-Api-Key=t0k=en\r\n\r\n# a comment\n  # indented\n X-Tenant = a b \n"), "X-Flag=1")
	want := [][2]string{{"X-Api-Key", "t0k=en"}, {"X-Tenant", " a b "}, {"X-Flag", "1"}}
	if err != nil || len(p.Headers) != len(want) {
		t.Fatalf("%+v, %v", p, err)
	}
	for i, h := range want {
		if p.Headers[i] != h {
			t.Errorf("header %d: %q, want %q", i, p.Headers[i], h)
		}
	}
	if p, err := parse(write("")); err != nil || len(p.Headers) != 0 {
		t.Errorf("empty file: %+v, %v", p, err)
	}
	for body, line := range map[string]string{
		"X-Ok=1\nAuthorization: Bearer s3cret-token\n": "line 2",
		"=s3cret-token":                  "line 1",
		"X-Ok=1\r\n \t=s3cret-token\r\n": "line 2",
	} {
		_, err := parse(write(body))
		e, ok := errs.As(err)
		if !ok || e.Code != errs.CodeInputDecodeFailed || !strings.Contains(e.Message, line) || strings.Contains(errText(err), "s3cret-token") {
			t.Errorf("%q: %v, want input_decode_failed on %s without the value", body, err, line)
		}
	}
	for _, n := range []int{readlimit.Secret, readlimit.Secret + 1} {
		_, err := parse(write("X-Big=" + strings.Repeat("v", n-len("X-Big="))))
		if n <= readlimit.Secret && err != nil {
			t.Errorf("%d bytes: %v", n, err)
		}
		if e, ok := errs.As(err); n > readlimit.Secret && (!ok || e.Code != errs.CodeInputDecodeFailed || strings.Contains(errText(err), "vvv")) {
			t.Errorf("%d bytes: %v, want input_decode_failed", n, err)
		}
	}
	if _, err := parse(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing --header-file was accepted")
	} else if e, _ := errs.As(err); e == nil || e.Code != errs.CodeInputFileOpenFailed {
		t.Errorf("missing file: %v", err)
	}
}
