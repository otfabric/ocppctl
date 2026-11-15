// SPDX-License-Identifier: MIT

//go:build unix

package csms

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// listenUnix creates the Unix socket at path with mode 0600: the umask is 0177 while the
// socket is bound, so that the file never has a wider mode, not even before the Chmod of
// listenControl. connect(2) needs write permission on the socket file. The umask is the
// process's: no other goroutine creates files while `server start` starts up.
func listenUnix(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}

// sharedDir returns a warning when the directory of the control socket at path can be
// written by other users and is not sticky: one of them could replace the socket with
// their own. Empty when it cannot.
func sharedDir(path string) string {
	dir := filepath.Dir(path)
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm()&0o022 == 0 || fi.Mode()&os.ModeSticky != 0 {
		return ""
	}
	return fmt.Sprintf("warning: the directory %s of the control socket can be written by other users, who could replace the socket: "+
		"choose a directory only you can write to with --control-socket (SECURITY.md)", dir)
}
