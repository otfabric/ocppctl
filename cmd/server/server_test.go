// SPDX-License-Identifier: MIT

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/cmd"
)

// srvBuffer is a bytes.Buffer safe for a writer and a reader at once.
type srvBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *srvBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *srvBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func srvRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := cmd.Run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func srvShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startServer runs `server start` with args until the test ends and returns the address
// it listens on and its stdout.
func srvStart(t *testing.T, args ...string) (string, *srvBuffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out, errOut := &srvBuffer{}, &srvBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- cmd.Run(ctx, append([]string{"server", "start", "--listen", "127.0.0.1:0", "--format", "json"}, args...), nil, out, errOut)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("server start exited %d: %s", code, errOut.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("server start did not stop")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sc := bufio.NewScanner(strings.NewReader(out.String()))
		for sc.Scan() {
			var ev struct{ Event, Address string }
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event == "listening" {
				return ev.Address, out
			}
		}
		select {
		case code := <-done:
			t.Fatalf("server start exited %d: %s %s", code, out.String(), errOut.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("server start did not listen: %s", errOut.String())
	return "", nil
}

func srvWaitFor(t *testing.T, out *srvBuffer, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), substr) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %s", substr, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartCallStations(t *testing.T) {
	sock := filepath.Join(srvShortDir(t), "c.sock")
	addr, out := srvStart(t, "--insecure-transport", "--no-auth", "--control-socket", sock)

	rec := ocpptest.NewRecorder().Answer("Reset", `{"status":"Accepted"}`).Answer("GetVariables", `{"getVariableResult":[{"attributeStatus":"Accepted","component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"},"attributeValue":"300"}]}`)
	c := ocpptest.Station(t, "ws://"+addr+"/CS1", client.WithVersions(ocpp.V201), client.WithHandler(ocpp.V201, rec))
	var resp json.RawMessage
	if err := c.Call(context.Background(), "Heartbeat", json.RawMessage(`{}`), &resp); err != nil {
		t.Fatal(err)
	}
	srvWaitFor(t, out, `"event":"answered"`)

	code, stdout, stderr := srvRun(t, "server", "stations", "--control-socket", sock, "--format", "json")
	if code != 0 || !strings.Contains(stdout, `"identity": "CS1"`) || !strings.Contains(stdout, `"version": "2.0.1"`) {
		t.Errorf("stations: %d %s %s", code, stdout, stderr)
	}
	if code, stdout, _ := srvRun(t, "server", "stations", "--control-socket", sock, "--format", "table"); code != 0 || !strings.Contains(stdout, "IDENTITY") {
		t.Errorf("stations table: %d %s", code, stdout)
	}

	reset := []string{"server", "call", "Reset", "--station", "CS1", "--control-socket", sock, "--payload", `{"type":"Immediate"}`}
	if code, _, stderr := srvRun(t, reset...); code != 2 || !strings.Contains(stderr, "--yes") {
		t.Errorf("without --yes: %d %s", code, stderr)
	}
	if code, stdout, stderr := srvRun(t, append(reset, "--yes")...); code != 0 || !strings.Contains(stdout, "Reset answered (OCPP 2.0.1") {
		t.Errorf("reset: %d %s %s", code, stdout, stderr)
	}
	if got := rec.Wait(t, "Reset"); string(got) != `{"type":"Immediate"}` {
		t.Errorf("the station got %s", got)
	}
	srvWaitFor(t, out, `"event":"call_result"`)

	get := []string{"server", "call", "GetVariables", "--station", "CS1", "--control-socket", sock, "--format", "json",
		"--payload", `{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}`}
	if code, stdout, stderr := srvRun(t, get...); code != 0 || !strings.Contains(stdout, `"attributeValue": "300"`) {
		t.Errorf("get variables: %d %s %s", code, stdout, stderr)
	}

	for name, tc := range map[string]struct {
		args []string
		code int
		want string
	}{
		"not connected":   {[]string{"server", "call", "Reset", "--station", "NONE", "--yes", "--control-socket", sock, "--payload", `{"type":"Immediate"}`, "--format", "json"}, 4, `"station_not_connected"`},
		"invalid payload": {[]string{"server", "call", "Reset", "--station", "CS1", "--yes", "--control-socket", sock, "--payload", `{"type":"Hard"}`, "--format", "json"}, 3, `"invalid_payload"`},
		"call error":      {[]string{"server", "call", "ClearCache", "--station", "CS1", "--yes", "--control-socket", sock, "--format", "json"}, 6, `"NotImplemented"`},
		"no socket":       {[]string{"server", "stations", "--control-socket", filepath.Join(srvShortDir(t), "none.sock")}, 4, "control socket"},
		"no station flag": {[]string{"server", "call", "Reset", "--control-socket", sock}, 2, "station"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := srvRun(t, tc.args...)
			if code != tc.code || !strings.Contains(stdout+stderr, tc.want) {
				t.Errorf("exit %d (want %d): %s %s", code, tc.code, stdout, stderr)
			}
		})
	}
}

func TestStartRefusals(t *testing.T) {
	if code, _, stderr := srvRun(t, "server", "start", "--insecure-transport"); code != 3 || !strings.Contains(stderr, "--no-auth") {
		t.Errorf("no authentication: %d %s", code, stderr)
	}
	if code, _, stderr := srvRun(t, "server", "start", "--no-auth"); code != 3 || !strings.Contains(stderr, "--insecure-transport") {
		t.Errorf("no TLS: %d %s", code, stderr)
	}
	model := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(model, []byte("boot: {status: Maybe}\nbogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := srvRun(t, "server", "start", "--no-auth", "--insecure-transport", "--config", model, "--format", "json"); code != 3 || !strings.Contains(stdout, `"invalid_config"`) {
		t.Errorf("bad model: %d %s", code, stdout)
	}
	if code, _, _ := srvRun(t, "server", "start", "--no-auth", "--insecure-transport", "--format", "xml"); code != 3 {
		t.Errorf("bad format: %d", code)
	}
}

func TestStartTable(t *testing.T) {
	var out, errOut bytes.Buffer
	code := cmd.Run(context.Background(), []string{"server", "start", "--listen", "127.0.0.1:0", "--insecure-transport", "--no-auth",
		"--control-socket", "", "--duration", "50ms", "--format", "table", "--debug"}, nil, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "EVENT") || !strings.Contains(out.String(), "listening") || !strings.Contains(out.String(), "stopping") {
		t.Errorf("table: %d %s %s", code, out.String(), errOut.String())
	}
	out.Reset()
	code = cmd.Run(context.Background(), []string{"server", "start", "--listen", "127.0.0.1:0", "--insecure-transport", "--no-auth",
		"--control-socket", "", "--duration", "50ms"}, nil, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "listening") {
		t.Errorf("text: %d %s", code, out.String())
	}
}

func TestGenerateConfig(t *testing.T) {
	code, stdout, _ := srvRun(t, "server", "generate-config")
	if code != 0 || !strings.Contains(stdout, "boot:") || !strings.HasPrefix(stdout, "#") {
		t.Fatalf("stdout: %d %s", code, stdout)
	}
	dir := t.TempDir()
	for _, f := range []string{"yaml", "json"} {
		path := filepath.Join(dir, "csms."+f)
		if code, _, stderr := srvRun(t, "server", "generate-config", "--format", f, "--output", path); code != 0 || !strings.Contains(stderr, "wrote") {
			t.Fatalf("%s: %d %s", f, code, stderr)
		}
		if code, _, stderr := srvRun(t, "server", "generate-config", "--format", f, "--output", path); code != 3 || !strings.Contains(stderr, "--force") {
			t.Errorf("%s exists: %d %s", f, code, stderr)
		}
		if code, _, _ := srvRun(t, "server", "generate-config", "--format", f, "--output", path, "--force"); code != 0 {
			t.Errorf("%s --force: %d", f, code)
		}
		// The generated model loads.
		if code, stdout, stderr := srvRun(t, "server", "start", "--listen", "127.0.0.1:0", "--insecure-transport", "--no-auth",
			"--control-socket", "", "--duration", "20ms", "--config", path); code != 0 {
			t.Errorf("%s does not load: %d %s %s", f, code, stdout, stderr)
		}
	}
	if code, _, _ := srvRun(t, "server", "generate-config", "--format", "xml"); code != 3 {
		t.Errorf("xml: %d", code)
	}
	if code, _, _ := srvRun(t, "server", "generate-config", "--output", filepath.Join(dir, "no", "such", "dir.yaml")); code != 10 {
		t.Errorf("unwritable: %d", code)
	}
	if code, stdout, _ := srvRun(t, "server"); code != 0 || !strings.Contains(stdout, "generate-config") {
		t.Errorf("help: %d %s", code, stdout)
	}
}
