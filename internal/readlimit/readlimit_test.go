// SPDX-License-Identifier: MIT

package readlimit

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRead(t *testing.T) {
	for _, tc := range []struct {
		n, limit int
		large    bool
	}{{0, 0, false}, {1, 0, true}, {0, 1, false}, {9, 10, false}, {10, 10, false}, {11, 10, true}, {100, 10, true}} {
		b, err := Read(strings.NewReader(strings.Repeat("x", tc.n)), int64(tc.limit))
		var tl *TooLargeError
		switch {
		case tc.large && (!errors.As(err, &tl) || tl.Limit != int64(tc.limit) || b != nil):
			t.Errorf("%d of %d: %d bytes, %v, want a TooLargeError", tc.n, tc.limit, len(b), err)
		case !tc.large && (err != nil || len(b) != tc.n):
			t.Errorf("%d of %d: %d bytes, %v", tc.n, tc.limit, len(b), err)
		}
	}
	boom := errors.New("boom")
	if _, err := Read(iotest.ErrReader(boom), 10); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
}

// Reading stops one byte past the limit.
func TestReadStopsPastTheLimit(t *testing.T) {
	r := strings.NewReader(strings.Repeat("x", 100))
	if _, err := Read(r, 10); err == nil {
		t.Fatal("no error")
	}
	if rest, _ := io.ReadAll(r); len(rest) != 89 {
		t.Errorf("%d bytes left unread, want 89", len(rest))
	}
}

func TestFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := File(f, 3); err != nil || string(b) != "abc" {
		t.Errorf("%q, %v", b, err)
	}
	if _, err := File(f, 2); err == nil || err.Error() != "larger than 2 bytes" {
		t.Errorf("over the limit: %v", err)
	}
	if _, err := File(f+"x", 2); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 bytes", 1023: "1023 bytes", 1 << 10: "1 KiB", Payload: "68 KiB",
		Secret: "64 KiB", Config: "4 MiB", 1<<20 + 1: "1048577 bytes", 3 << 20: "3 MiB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}
