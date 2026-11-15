// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
)

// A token in --header-file reaches the handshake and shows up in no output: not in the
// result, the --debug trace, or an error.
func TestHeaderFileToken(t *testing.T) {
	const token = "Bearer hdr-t0ken-1f2e3d"
	var mu sync.Mutex
	var got []string
	csms := testcsms.Start(t, server.WithAuthenticator(server.AuthenticatorFunc(func(_ context.Context, r server.ConnectRequest) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r.Request.Header.Get("X-Api-Key"))
		return nil
	})))
	dir := t.TempDir()
	file := filepath.Join(dir, "headers")
	if err := os.WriteFile(file, []byte("# the CSMS's API key\nX-Api-Key="+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"call", "Heartbeat", "--debug"},
		{"call", "Heartbeat", "--format", "json"},
		{"probe", "--debug", "--format", "table"},
	} {
		r := station(t, csms, "CSH", args[0], append(args[1:], "--header-file", file)...).want(t, errs.ExitOK)
		if strings.Contains(r.stdout+r.stderr, "t0ken") {
			t.Errorf("%v: the token is in the output:\n%s\n%s", args, r.stdout, r.stderr)
		}
	}
	t.Setenv("OCPPCTL_HEADER_FILE", file)
	station(t, csms, "CSH", "call", "Heartbeat").want(t, errs.ExitOK)
	mu.Lock()
	if len(got) != 4 || got[0] != token || got[3] != token {
		t.Errorf("the CSMS received %q, want the token on every handshake", got)
	}
	mu.Unlock()

	// A malformed line is refused before connecting, by its number.
	if err := os.WriteFile(file, []byte("X-Api-Key: "+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := station(t, csms, "CSH", "call", "Heartbeat", "--debug").want(t, errs.ExitInvalidInput).errHas(t, "line 1")
	if strings.Contains(r.stdout+r.stderr, "t0ken") {
		t.Errorf("the token is in the error:\n%s", r.stderr)
	}
}
