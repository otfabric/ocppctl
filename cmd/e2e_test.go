// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// TestEndToEnd runs ocppctl's station against ocppctl's CSMS, in every version.
func TestEndToEnd(t *testing.T) {
	port := freePort(t)
	sock, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sock) })
	sock = filepath.Join(sock, "s.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srvDone := make(chan result, 1)
	go func() {
		srvDone <- runCtx(t, ctx, "server", "start", "--listen", "127.0.0.1:"+port, "--no-auth", "--insecure-transport",
			"--control-socket", sock, "--format", "json")
	}()
	url := "ws://127.0.0.1:" + port + "/"
	eventually(t, "the server", func() bool {
		return run(t, "client", "probe", "--url", url+"P", "--insecure-transport").code == errs.ExitOK
	})

	run(t, "client", "script", "../examples/scenarios/smoke-1.6.yaml", "--url", url+"E16", "--insecure-transport", "--var", "token=AB").want(t, errs.ExitOK)
	run(t, "client", "script", "../examples/scenarios/smoke-2.0.1.yaml", "--url", url+"E201", "--insecure-transport", "--var", "token=AB").want(t, errs.ExitOK)

	runCtx2, stopRun := context.WithCancel(ctx)
	runDone := make(chan result, 1)
	go func() {
		runDone <- runCtx(t, runCtx2, "client", "run", "--url", url+"SIM1", "--insecure-transport", "--format", "json")
	}()
	eventually(t, "SIM1", func() bool {
		return strings.Contains(run(t, "server", "stations", "--control-socket", sock).stdout, "SIM1")
	})
	run(t, "server", "call", "GetVariables", "--station", "SIM1", "--control-socket", sock,
		"--payload", `{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}`).
		want(t, errs.ExitOK).has(t, `"attributeValue": "300"`)
	run(t, "server", "call", "Reset", "--station", "SIM1", "--control-socket", sock, "--payload", `{"type":"Immediate"}`).want(t, errs.ExitUsage)
	run(t, "server", "call", "Reset", "--station", "SIM1", "--control-socket", sock, "--payload", `{"type":"Immediate"}`, "--yes").
		want(t, errs.ExitOK).has(t, "Accepted")
	time.Sleep(300 * time.Millisecond)
	stopRun()
	r := (<-runDone).want(t, errs.ExitOK)
	if n := strings.Count(r.stdout, `"event":"booted"`); n != 2 {
		t.Fatalf("booted %d times, want 2 (Reset):\n%s", n, r.stdout)
	}
	cancel()
	(<-srvDone).want(t, errs.ExitOK).has(t, `"event":"connected"`, `"action":"TransactionEvent"`, `"action":"StopTransaction"`)
}

func TestClientRun(t *testing.T) {
	csms := testcsms.Start(t)
	r := station(t, csms, "R1", "run", "--duration", "500ms", "--format", "json").want(t, errs.ExitOK)
	lines := r.lines(t)
	if len(lines) < 4 || lines[0]["event"] != "connected" || lines[0]["station"] != "R1" {
		t.Fatalf("events: %v", lines)
	}
	if ts, _ := lines[0]["time"].(string); len(ts) != len("2026-01-01T00:00:00.000Z") {
		t.Fatalf("time with milliseconds: %q", ts)
	}
	run(t, "client", "run", "--url", csms.URL+"L{01..03}", "--insecure-transport", "--duration", "500ms", "--stagger", "10ms", "--format", "table").
		want(t, errs.ExitOK).has(t, "EVENT", "L01", "L03").errHas(t, "summary: 3 stations, 3 booted, 0 failed")
	station(t, csms, "R2", "run", "--duration", "300ms").want(t, errs.ExitOK).has(t, "R2 booted")

	m := filepath.Join(t.TempDir(), "m.yaml")
	run(t, "client", "generate-config", "--output", m).want(t, errs.ExitOK).errHas(t, "wrote")
	run(t, "client", "generate-config", "--output", m).want(t, errs.ExitInvalidInput).errHas(t, "--force")
	run(t, "client", "generate-config", "--output", m, "--force").want(t, errs.ExitOK)
	station(t, csms, "R3", "run", "--config", m, "--duration", "200ms").want(t, errs.ExitOK)
	j := filepath.Join(t.TempDir(), "m.json")
	run(t, "client", "generate-config", "--format", "json", "--output", j).want(t, errs.ExitOK)
	station(t, csms, "R4", "run", "--config", j, "--duration", "200ms", "--ocpp-version", "1.6").want(t, errs.ExitOK)
	run(t, "client", "generate-config").want(t, errs.ExitOK).has(t, "evses:")
	run(t, "client", "generate-config", "--format", "toml").want(t, errs.ExitInvalidInput)

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("station: {vendor: V}\nevses: []\nanswers: {BootNotification: accept, Reset: {status: Bogus}, Nope: accept, ClearCache: maybe}\nbogus: 1\n"), 0o600)
	station(t, csms, "R5", "run", "--config", bad).want(t, errs.ExitInvalidInput)
	_ = os.WriteFile(bad, []byte("station: {vendor: V}\nevses: []\nanswers: {BootNotification: accept, Reset: {status: Bogus}, Nope: accept, ClearCache: maybe}\n"), 0o600)
	station(t, csms, "R5", "run", "--config", bad).want(t, errs.ExitInvalidInput).
		errHas(t, "vendor and model", "at least one EVSE", "BootNotification is sent by the station", "Reset: the response", "Nope", "maybe")
	station(t, csms, "R5", "run", "--config", "/nonexistent.yaml").want(t, errs.ExitInvalidInput)
	station(t, csms, "R5", "run", "--duration", "x").want(t, errs.ExitInvalidInput)
	run(t, "client", "run", "--url", csms.URL+"X{1..2}{1..2}", "--insecure-transport").want(t, errs.ExitInvalidInput)
	run(t, "client", "run", "--url", csms.URL+"X{5..1}", "--insecure-transport").want(t, errs.ExitInvalidInput)
	run(t, "client", "run", "--url", "ws://127.0.0.1:1/X", "--insecure-transport", "--connect-timeout", "1s").want(t, errs.ExitTransport)
}
