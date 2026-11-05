// SPDX-License-Identifier: MIT

package script

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/answer"
	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

// slow answers Heartbeat after a delay, everything else at once.
type slow struct {
	delay time.Duration
	next  ocpp.Dispatcher
}

func (s slow) Dispatch(ctx context.Context, p ocpp.Peer, action string, req json.RawMessage) (json.RawMessage, error) {
	if action == "Heartbeat" {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.next.Dispatch(ctx, p, action, req)
}

func writeScenario(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func runScenario(t *testing.T, url, body string, mod func(*Config)) (*types.ScriptResult, string, error) {
	t.Helper()
	cfg := &Config{Target: conn.NewTarget(), File: writeScenario(t, body)}
	cfg.URL, cfg.InsecureTransport = url, true
	if mod != nil {
		mod(cfg)
	}
	sc, p, err := Prepare(cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	res, err := Run(ctx, cfg, sc, p, &stderr)
	return res, stderr.String(), err
}

// The timeout of a call step is how long it waits for its response, also when it is
// longer than --timeout: --timeout is only the default (docs/scenarios.md).
func TestStepTimeoutLongerThanDefault(t *testing.T) {
	v := ocpp.V201
	csms := testcsms.Start(t, server.WithHandler(v, slow{delay: 400 * time.Millisecond, next: testcsms.Answers(v)}))
	res, stderr, err := runScenario(t, csms.URL+"CST", `version: "2.0.1"
steps:
  - call: Heartbeat
    timeout: 10s
`, func(c *Config) { c.Timeout = "100ms" })
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || res.Passed != 1 {
		t.Fatalf("a step with timeout 10s failed after --timeout 100ms: %+v\n%s", res.Steps, stderr)
	}
	// And a step without a timeout of its own still has --timeout.
	res, _, err = runScenario(t, csms.URL+"CST2", `version: "2.0.1"
steps:
  - call: Heartbeat
`, func(c *Config) { c.Timeout = "100ms" })
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || !strings.Contains(res.Steps[0].Message, "no response within 100ms") {
		t.Fatalf("--timeout as the default: %+v", res.Steps)
	}
}

// The dispatcher answers a CALL that a step waits for but that does not conform to its
// schema with the CALLERROR of the violation, and records SENDs and CALLs of nobody as
// unexpected (and as noise during expect_no_call).
func TestDispatcherDirect(t *testing.T) {
	v := ocpp.V201
	r := &runner{sc: &Scenario{Steps: []Step{{WaitFor: "Reset"}}}, inbox: make(chan *incoming, 1), ended: make(chan struct{})}
	d := &dispatcher{r: r, v: v, def: &answer.Default{Version: v}}
	ctx := context.Background()
	if _, err := d.Dispatch(ctx, nil, "Reset", json.RawMessage(`{"type":"Sideways"}`)); err == nil {
		t.Fatal("an invalid waited CALL was accepted")
	} else if ce, ok := err.(*ocpp.CallError); !ok || ce.Code == "" {
		t.Fatalf("an invalid waited CALL: %v", err)
	}
	r.quiet = true
	if err := d.DispatchSend(ctx, nil, "NotifyPeriodicEventStream", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	resp, err := d.Dispatch(ctx, nil, "ClearCache", json.RawMessage(`{}`))
	if err != nil || string(resp) != `{"status":"Accepted"}` {
		t.Fatalf("an unexpected CALL: %s, %v", resp, err)
	}
	if len(r.unexpected) != 2 || r.unexpected[0].Response != nil || len(r.noise) != 2 {
		t.Fatalf("unexpected %+v, noise %v", r.unexpected, r.noise)
	}
	// A waited CALL after the scenario ended gets the default answer.
	close(r.ended)
	if resp, err := d.Dispatch(ctx, nil, "Reset", json.RawMessage(`{"type":"Immediate"}`)); err != nil || string(resp) != `{"status":"Accepted"}` {
		t.Fatalf("after the end: %s, %v", resp, err)
	}
}

func TestDefaultAnswer(t *testing.T) {
	// GetVariables needs a result per request: neither {} nor status Accepted fits.
	if _, err := defaultAnswer(ocpp.V201, "GetVariables"); err == nil {
		t.Fatal("a default answer for GetVariables")
	} else if ce, ok := err.(*ocpp.CallError); !ok || ce.Code != ocpp.NotSupported {
		t.Fatalf("%v", err)
	}
	if resp, err := defaultAnswer(ocpp.V16, "ClearCache"); err != nil || string(resp) != `{"status":"Accepted"}` {
		t.Fatalf("ClearCache: %s, %v", resp, err)
	}
}

// Steps that act on the connection: a call after a disconnect fails, a second disconnect
// fails, and a reconnect makes calls work again.
func TestConnectionSteps(t *testing.T) {
	csms := testcsms.Start(t)
	res, _, err := runScenario(t, csms.URL+"CSC", `version: "1.6"
steps:
  - disconnect: true
  - call: Heartbeat
  - disconnect: true
  - send: NotifyPeriodicEventStream
    raw: true
  - reconnect: true
  - call: Heartbeat
  - reconnect: true
  - sleep: 1ms
`, func(c *Config) { c.KeepGoing = true })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range res.Steps {
		got = append(got, s.Result)
	}
	if strings.Join(got, ",") != "pass,fail,fail,fail,pass,pass,pass,pass" {
		t.Fatalf("results %v: %+v", got, res.Steps)
	}
}
