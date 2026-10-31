// SPDX-License-Identifier: MIT

package main

import (
	"os"

	"github.com/otfabric/ocppctl/cmd"
)

// Injected at link time (see Makefile): -X main.version=... etc.
var (
	version   = "dev"
	tag       = ""
	commit    = ""
	buildDate = ""
)

// exit ends the process; a test replaces it.
var exit = os.Exit

func main() {
	cmd.SetBuildMeta(version, tag, commit, buildDate)
	exit(cmd.Execute())
}
