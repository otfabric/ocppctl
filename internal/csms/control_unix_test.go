// SPDX-License-Identifier: MIT

//go:build unix

package csms

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The control socket has mode 0600 from the moment it exists, whatever the umask: nobody
// else can connect to it before its mode is set.
func TestControlSocketCreatedPrivate(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	var mode os.FileMode
	var statErr error
	listened = func(path string) {
		var fi os.FileInfo
		if fi, statErr = os.Lstat(path); statErr == nil {
			mode = fi.Mode()
		}
	}
	t.Cleanup(func() { listened = func(string) {} })
	sock := filepath.Join(shortDir(t), "u.sock")
	ct, err := listenControl(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	ct.close()
	if statErr != nil || mode&os.ModeSocket == 0 || mode.Perm()&^0o600 != 0 {
		t.Fatalf("the socket was created with mode %v (%v), want 0600 at most", mode, statErr)
	}
	if cur := syscall.Umask(0); cur != 0 {
		t.Errorf("the umask was left at %#o, want it restored", cur)
	}
}

// A control socket in a directory that others can write to, and that is not sticky, is
// warned about.
func TestControlSocketSharedDirectory(t *testing.T) {
	dir := shortDir(t)
	for mode, warned := range map[os.FileMode]bool{0o700: false, 0o755: false, 0o770: true, 0o777: true, 0o777 | os.ModeSticky: false} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		w := sharedDir(filepath.Join(dir, "c.sock"))
		if (w != "") != warned || (warned && !strings.Contains(w, dir)) {
			t.Errorf("directory mode %v: %q", mode, w)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if w := sharedDir(filepath.Join(dir, "missing", "c.sock")); w != "" {
		t.Errorf("a directory that does not exist: %q", w)
	}
}
