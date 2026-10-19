// SPDX-License-Identifier: MIT

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
)

// listening returns the address of the "listening" event of a server started with
// --format json.
func listening(t *testing.T, l *live) string {
	t.Helper()
	l.waitStdout(t, `"event":"listening"`)
	for _, line := range strings.Split(l.stdout.String(), "\n") {
		var ev struct{ Event, Address string }
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "listening" {
			return ev.Address
		}
	}
	t.Fatal("no listening event")
	return ""
}

// TestEndToEnd runs ocppctl's station against ocppctl's CSMS, in every version.
func TestEndToEnd(t *testing.T) {
	dir, err := os.MkdirTemp("", "oc") // short: Unix socket paths are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	srv := start(t, "server", "start", "--listen", "127.0.0.1:0", "--no-auth", "--insecure-transport",
		"--control-socket", sock, "--format", "json")
	url := "ws://" + listening(t, srv) + "/"

	run(t, "client", "script", "../examples/scenarios/smoke-1.6.yaml", "--url", url+"E16", "--insecure-transport", "--var", "token=AB").want(t, errs.ExitOK)
	run(t, "client", "script", "../examples/scenarios/smoke-2.0.1.yaml", "--url", url+"E201", "--insecure-transport", "--var", "token=AB").want(t, errs.ExitOK)

	sim := start(t, "client", "run", "--url", url+"SIM1", "--insecure-transport", "--format", "json")
	sim.waitStdout(t, `"event":"booted"`)
	run(t, "server", "stations", "--control-socket", sock).want(t, errs.ExitOK).has(t, "SIM1")
	run(t, "server", "call", "GetVariables", "--station", "SIM1", "--control-socket", sock,
		"--payload", `{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}`).
		want(t, errs.ExitOK).has(t, `"attributeValue": "300"`)
	run(t, "server", "call", "Reset", "--station", "SIM1", "--control-socket", sock, "--payload", `{"type":"Immediate"}`).want(t, errs.ExitUsage)
	run(t, "server", "call", "Reset", "--station", "SIM1", "--control-socket", sock, "--payload", `{"type":"Immediate"}`, "--yes").
		want(t, errs.ExitOK).has(t, "Accepted")
	eventually(t, "the second boot", func() bool { return strings.Count(sim.stdout.String(), `"event":"booted"`) == 2 })
	sim.stop(t).want(t, errs.ExitOK).has(t, `"event":"reset"`)
	srv.stop(t).want(t, errs.ExitOK).has(t, `"event":"connected"`, `"action":"TransactionEvent"`, `"action":"StopTransaction"`)
}

func TestClientRun(t *testing.T) {
	csms := testcsms.Start(t)
	l := start(t, "client", "run", "--url", csms.URL+"R1", "--insecure-transport", "--format", "json")
	l.waitStdout(t, `"event":"booted"`)
	lines := l.stop(t).want(t, errs.ExitOK).lines(t)
	if len(lines) < 4 || lines[0]["event"] != "connected" || lines[0]["station"] != "R1" {
		t.Fatalf("events: %v", lines)
	}
	if ts, _ := lines[0]["time"].(string); len(ts) != len("2026-01-01T00:00:00.000Z") {
		t.Fatalf("time with milliseconds: %q", ts)
	}
	l = start(t, "client", "run", "--url", csms.URL+"L{01..03}", "--insecure-transport", "--stagger", "10ms", "--format", "table")
	eventually(t, "three boots", func() bool { return strings.Count(l.stdout.String(), " booted ") == 3 })
	l.stop(t).want(t, errs.ExitOK).has(t, "EVENT", "L01", "L03").errHas(t, "summary: 3 stations, 3 booted, 0 failed")
	l = start(t, "client", "run", "--url", csms.URL+"R2", "--insecure-transport")
	l.waitStdout(t, "R2 booted")
	l.stop(t).want(t, errs.ExitOK)
	// --duration ends the run normally, even when it ends before the station is connected
	station(t, csms, "R2", "run", "--duration", "1ms").want(t, errs.ExitOK)
	station(t, csms, "R2", "run", "--duration", "300ms").want(t, errs.ExitOK)

	m := filepath.Join(t.TempDir(), "m.yaml")
	run(t, "client", "generate-config", "--output", m).want(t, errs.ExitOK).errHas(t, "wrote")
	run(t, "client", "generate-config", "--output", m).want(t, errs.ExitInvalidInput).errHas(t, "--force")
	run(t, "client", "generate-config", "--output", m, "--force").want(t, errs.ExitOK)
	l = start(t, "client", "run", "--url", csms.URL+"R3", "--insecure-transport", "--config", m)
	l.waitStdout(t, "R3 booted")
	l.stop(t).want(t, errs.ExitOK)
	j := filepath.Join(t.TempDir(), "m.json")
	run(t, "client", "generate-config", "--format", "json", "--output", j).want(t, errs.ExitOK)
	l = start(t, "client", "run", "--url", csms.URL+"R4", "--insecure-transport", "--config", j, "--ocpp-version", "1.6")
	l.waitStdout(t, "R4 booted")
	l.stop(t).want(t, errs.ExitOK)
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
