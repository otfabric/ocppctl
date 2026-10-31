// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// runMain runs main with args and returns its exit code and standard output.
func runMain(t *testing.T, args ...string) (int, string) {
	t.Helper()
	oldArgs, oldStdout, oldExit := os.Args, os.Stdout, exit
	defer func() { os.Args, os.Stdout, exit = oldArgs, oldStdout, oldExit }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	code := -1
	exit = func(c int) { code = c }
	os.Args, os.Stdout = append([]string{"ocppctl"}, args...), w
	main()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

func TestMainPassesBuildMetaAndExitCode(t *testing.T) {
	old := version
	defer func() { version = old }()
	version = "v9.8.7-test"
	code, out := runMain(t, "version")
	if code != 0 || !strings.Contains(out, "v9.8.7-test") {
		t.Fatalf("version: exit %d, output %q", code, out)
	}
	if code, _ := runMain(t, "no-such-command"); code == 0 {
		t.Fatal("an unknown command exited 0")
	}
}
