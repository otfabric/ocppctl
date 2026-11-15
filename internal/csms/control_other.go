// SPDX-License-Identifier: MIT

//go:build !unix

package csms

import "net"

// listenUnix creates the Unix socket at path. File modes are Unix ones: listenControl's
// Chmod is all there is here.
func listenUnix(path string) (net.Listener, error) { return net.Listen("unix", path) }

// sharedDir warns about a shared directory on Unix only, where modes say who can write.
func sharedDir(string) string { return "" }
