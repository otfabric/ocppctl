// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

func scenario(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestScriptExamples(t *testing.T) {
	csms := testcsms.Start(t)
	for _, name := range []string{"smoke-1.6", "smoke-2.0.1"} {
		t.Run(name, func(t *testing.T) {
			var res types.ScriptResult
			station(t, csms, "CS1", "script", "../examples/scenarios/"+name+".yaml", "--var", "token=04A1B2C3D4E5F6", "--format", "json").
				want(t, errs.ExitOK).decode(t, &res)
			if res.Failed != 0 || res.Passed != len(res.Steps) || res.Scenario != name {
				t.Fatalf("%s: %+v", name, res)
			}
		})
	}
	stop := csms.Recorder(ocpp.V16).Received("StopTransaction")
	if len(stop) != 1 || !strings.Contains(string(stop[0]), `"transactionId":7`) {
		t.Fatalf("the saved transaction id: %s", stop)
	}
	meter := csms.Recorder(ocpp.V16).Received("MeterValues")
	if len(meter) != 1 || !strings.Contains(string(meter[0]), `"transactionId":7`) {
		t.Fatalf("the payload file: %s", meter)
	}
}

func TestScriptWaitFor(t *testing.T) {
	csms := testcsms.Start(t)
	done := make(chan result, 1)
	go func() {
		done <- station(t, csms, "CSW", "script", "../examples/scenarios/remote-start-2.0.1.yaml", "--format", "json")
	}()
	sess := ocpptest.Session(t, csms.Server, "CSW")
	csms.Recorder(ocpp.V201).Wait(t, "BootNotification")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var resp map[string]any
	if err := sess.Call(ctx, "RequestStartTransaction", json.RawMessage(`{"remoteStartId":42,"idToken":{"idToken":"TOK","type":"ISO14443"}}`), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "Accepted" {
		t.Fatalf("answer: %v", resp)
	}
	var res types.ScriptResult
	(<-done).want(t, errs.ExitOK).decode(t, &res)
	if res.Passed != 4 {
		t.Fatalf("%+v", res)
	}
	te := csms.Recorder(ocpp.V201).Wait(t, "TransactionEvent")
	if !strings.Contains(string(te), `"remoteStartId":42`) || !strings.Contains(string(te), `"idToken":"TOK"`) {
		t.Fatalf("saved variables: %s", te)
	}
}

func TestScriptFailures(t *testing.T) {
	csms := testcsms.Start(t)
	f := scenario(t, `
version: "2.0.1"
steps:
  - call: Heartbeat
    expect: {currentTime: "1999"}
  - call: Heartbeat
`)
	var res types.ScriptResult
	station(t, csms, "CSF", "script", f, "--format", "json").want(t, errs.ExitPartial).decode(t, &res)
	if res.Failed != 1 || res.Skipped != 1 || !strings.Contains(res.Steps[0].Message, "want \"1999\"") {
		t.Fatalf("%+v", res)
	}
	station(t, csms, "CSF", "script", f, "--keep-going").want(t, errs.ExitPartial).has(t, "FAIL", "PASS", "1 passed, 1 failed")
	station(t, csms, "CSF", "script", f, "--format", "table").want(t, errs.ExitPartial).has(t, "RESULT", "fail")

	csms.Recorder(ocpp.V201).AnswerError("DataTransfer", ocpp.NewCallError(ocpp.NotImplemented, "no"))
	f = scenario(t, `
version: "2.0.1"
steps:
  - call: DataTransfer
    payload: {vendorId: x}
    expect_error: NotImplemented
  - call: DataTransfer
    payload: {vendorId: x}
    expect_error: any
  - call: DataTransfer
    payload: {vendorId: x}
    expect_error: GenericError
  - call: DataTransfer
    payload: {vendorId: x}
  - call: Heartbeat
    expect_error: any
  - call: Heartbeat
    expect:
      currentTime: {regex: "^2026"}
      interval: {present: false}
      nope: {present: true}
  - call: Heartbeat
    expect: {currentTime: {one_of: ["a", "b"]}}
  - call: Heartbeat
    save: {x: nope}
  - call: Heartbeat
    payload: {x: "${undefined}"}
  - call: Heartbeat
    payload: {x: 1}
  - call: Heartbeat
    raw: true
    payload: {x: 1}
  - wait_for: Reset
    timeout: 100ms
  - expect_no_call: 50ms
  - sleep: 10ms
  - disconnect: true
  - call: Heartbeat
  - disconnect: true
  - reconnect: true
  - reconnect: true
  - send: NotifyPeriodicEventStream
    raw: true
`)
	station(t, csms, "CSF", "script", f, "--keep-going", "--format", "json").want(t, errs.ExitPartial).decode(t, &res)
	want := "pass pass fail fail fail fail fail fail fail fail pass fail pass pass pass fail fail pass pass fail"
	var got []string
	for _, s := range res.Steps {
		got = append(got, s.Result)
	}
	if strings.Join(got, " ") != want {
		for _, s := range res.Steps {
			t.Logf("%d %s %s: %s", s.Index, s.Kind, s.Result, s.Message)
		}
		t.Fatalf("results\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestScriptUnexpectedAndNoise(t *testing.T) {
	csms := testcsms.Start(t)
	f := scenario(t, `
version: "1.6"
steps:
  - call: Heartbeat
  - expect_no_call: 1s
  - sleep: 300ms
`)
	done := make(chan result, 1)
	go func() { done <- station(t, csms, "CSN", "script", f, "--keep-going", "--format", "json") }()
	sess := ocpptest.Session(t, csms.Server, "CSN")
	csms.Recorder(ocpp.V16).Wait(t, "Heartbeat")
	time.Sleep(300 * time.Millisecond) // into expect_no_call
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp map[string]any
	if err := sess.Call(ctx, "ClearCache", json.RawMessage(`{}`), &resp); err != nil {
		t.Fatal(err)
	}
	var res types.ScriptResult
	(<-done).want(t, errs.ExitPartial).decode(t, &res)
	if res.Steps[1].Result != "fail" || !strings.Contains(res.Steps[1].Message, "ClearCache") || len(res.Unexpected) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestScriptInvalid(t *testing.T) {
	csms := testcsms.Start(t)
	for name, body := range map[string]string{
		"empty":        ``,
		"no steps":     `version: "2.0.1"`,
		"unknown":      "steps:\n  - call: Heartbeat\n    bogus: 1",
		"two kinds":    "steps:\n  - call: Heartbeat\n    sleep: 1s",
		"no kind":      "steps:\n  - name: x",
		"version":      "version: \"9\"\nsteps:\n  - sleep: 1s",
		"action":       "version: \"1.6\"\nsteps:\n  - call: Bogus",
		"direction":    "version: \"1.6\"\nsteps:\n  - call: Reset",
		"wait station": "version: \"1.6\"\nsteps:\n  - wait_for: Heartbeat",
		"send call":    "version: \"2.1\"\nsteps:\n  - send: Heartbeat",
		"call send":    "version: \"2.1\"\nsteps:\n  - call: NotifyPeriodicEventStream",
		"file":         "steps:\n  - call: Heartbeat\n    payload: \"@nope.json\"",
		"behaviour":    "steps:\n  - call: Heartbeat\n    payload: accept",
		"answer":       "steps:\n  - wait_for: Reset\n    answer: maybe",
		"answer call":  "steps:\n  - call: Heartbeat\n    answer: accept",
		"payload wait": "steps:\n  - wait_for: Reset\n    payload: {a: 1}",
		"sleep extra":  "steps:\n  - sleep: 1s\n    expect: {a: 1}",
		"send expect":  "steps:\n  - send: X\n    expect: {a: 1}",
		"both expects": "steps:\n  - call: Heartbeat\n    expect: {a: 1}\n    expect_error: any",
		"regex":        "steps:\n  - call: Heartbeat\n    expect: {a: {regex: \"(\"}}",
		"duration":     "steps:\n  - sleep: soon",
		"json":         "steps:\n  - call: Heartbeat\n    payload: \"{bad\"",
		"operator":     "steps:\n  - call: Heartbeat\n    expect: {a: {regex: x, bogus: 1}}",
	} {
		t.Run(name, func(t *testing.T) {
			station(t, csms, "CSI", "script", scenario(t, body)).want(t, errs.ExitInvalidInput)
		})
	}
	station(t, csms, "CSI", "script", "/nonexistent.yaml").want(t, errs.ExitInvalidInput)
	station(t, csms, "CSI", "script", scenario(t, "steps:\n  - sleep: 1ms"), "--var", "1bad=x").want(t, errs.ExitInvalidInput)
	run(t, "client", "script", scenario(t, "steps:\n  - sleep: 1ms"), "--url", "ws://127.0.0.1:1/X", "--insecure-transport").want(t, errs.ExitTransport)
}

func TestScriptWaitForAnswers(t *testing.T) {
	csms := testcsms.Start(t)
	f := scenario(t, `
version: "2.0.1"
steps:
  - wait_for: UnlockConnector
    answer: {status: Unlocked}
    expect: {evseId: 1}
  - wait_for: Reset
    answer: reject
  - wait_for: ClearCache
    answer: not-supported
  - wait_for: ChangeAvailability
    answer: not-implemented
  - wait_for: GetLocalListVersion
  - wait_for: UnlockConnector
    answer: {status: "bogus"}
  - wait_for: TriggerMessage
    answer: accept
`)
	done := make(chan result, 1)
	go func() { done <- station(t, csms, "CSA", "script", f, "--keep-going", "--format", "json") }()
	sess := ocpptest.Session(t, csms.Server, "CSA")
	call := func(action, req string) (map[string]any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var resp map[string]any
		err := sess.Call(ctx, action, json.RawMessage(req), &resp)
		return resp, err
	}
	// sent out of order: the CALLs wait for their steps
	go func() { _, _ = call("Reset", `{"type":"Immediate"}`) }()
	if r, err := call("UnlockConnector", `{"evseId":1,"connectorId":1}`); err != nil || r["status"] != "Unlocked" {
		t.Fatalf("UnlockConnector: %v %v", r, err)
	}
	if _, err := call("ClearCache", `{}`); err == nil {
		t.Fatal("ClearCache: want NotSupported")
	}
	if _, err := call("ChangeAvailability", `{"operationalStatus":"Operative"}`); err == nil {
		t.Fatal("ChangeAvailability: want NotImplemented")
	}
	if r, err := call("GetLocalListVersion", `{}`); err != nil || r["versionNumber"] == nil && r["status"] == nil {
		t.Logf("GetLocalListVersion: %v %v", r, err)
	}
	_, _ = call("UnlockConnector", `{"evseId":1,"connectorId":1}`)
	if r, err := call("TriggerMessage", `{"requestedMessage":"Heartbeat"}`); err != nil || r["status"] != "Accepted" {
		d := <-done
		t.Fatalf("TriggerMessage: %v %v\n%s\n%s", r, err, d.stdout, d.stderr)
	}
	var res types.ScriptResult
	(<-done).want(t, errs.ExitPartial).decode(t, &res)
	var got []string
	for _, s := range res.Steps {
		got = append(got, s.Result)
	}
	if strings.Join(got, " ") != "pass pass pass pass pass fail pass" {
		for _, s := range res.Steps {
			t.Logf("%d %s %s: %s", s.Index, s.Action, s.Result, s.Message)
		}
		t.Fatalf("results %v", got)
	}
}
