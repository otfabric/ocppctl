// SPDX-License-Identifier: MIT

package csms

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/types"
)

var allVersions = []ocpp.Version{ocpp.V21, ocpp.V201, ocpp.V16}

func TestDefaultModel(t *testing.T) {
	for _, f := range []string{"yaml", "json"} {
		data, err := Generate(f)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := m.Validate(allVersions); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if m.Boot.Status != "Accepted" || *m.Boot.Interval != 300 || *m.Transactions.FirstID != 1 {
			t.Errorf("%s: %+v", f, m)
		}
	}
	if _, err := Generate("xml"); err == nil {
		t.Error("xml accepted")
	}
}

func TestModelValidation(t *testing.T) {
	bad := `
boot: {status: Maybe, interval: -1}
stations:
  - identity: ""
  - identity: A
    boot_status: Later
  - identity: A
authorization: {tokens: [" "]}
transactions: {first_id: 0}
actions:
  - answer: reject
  - action: Heartbeat
    answer: reject
  - action: Reset
    response: {status: Accepted}
  - action: NotifyPeriodicEventStream
    versions: ["2.1"]
    response: {}
  - action: DataTransfer
    answer: maybe
  - action: DataTransfer
  - action: DataTransfer
    answer: not-supported
    response: {}
  - action: Bogus
    response: {}
  - action: Heartbeat
    versions: ["9"]
    response: {}
  - action: StartTransaction
    versions: ["2.0.1"]
    response: {}
`
	m, err := Parse([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Validate(allVersions)
	if err == nil {
		t.Fatal("a bad model was accepted")
	}
	for _, want := range []string{
		`boot.status "Maybe"`, "boot.interval -1", "stations[0]: identity is required", `identity "A" is listed twice`,
		`boot_status "Later"`, "authorization.tokens[0]", "transactions.first_id 0", "actions[0]: action is required",
		"actions[1] (Heartbeat): the answer is not a valid Heartbeat response", "Reset is sent by the CSMS",
		"is a SEND", `answer "maybe"`, "actions[5] (DataTransfer): response is required", "response goes with answer: response only",
		"actions[7] (Bogus): not an action", "unknown OCPP version", `"StartTransaction" is not an action of OCPP 2.0.1`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}

	for name, src := range map[string]string{
		"unknown field": "boot: {status: Accepted, intervall: 5}\n",
		"empty":         "",
		"syntax":        "boot: [\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none.yaml"), allVersions); err == nil {
		t.Error("a missing file loaded")
	}
	if _, err := Load(writeFile(t, "m.yaml", "boot: {x: 1}\n"), allVersions); err == nil || !strings.Contains(err.Error(), "m.yaml") {
		t.Errorf("an unknown field: %v", err)
	}
	if _, err := Load(writeFile(t, "m.yaml", "boot: {status: No}\n"), allVersions); err == nil {
		t.Error("an invalid model loaded")
	}
}

func TestClientSide(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		cfg  func(*CallConfig)
		kind errs.Kind
		code string
	}{
		"no station":   {func(c *CallConfig) { c.Station = "" }, errs.KindUsage, errs.CodeInvalidInput},
		"unknown":      {func(c *CallConfig) { c.Action = "Rest" }, errs.KindInvalidInput, errs.CodeUnknownAction},
		"no --yes":     {func(c *CallConfig) { c.Yes = false }, errs.KindUsage, errs.CodeConfirmationRequired},
		"timeout":      {func(c *CallConfig) { c.Timeout = "soon" }, errs.KindInvalidInput, errs.CodeInvalidInput},
		"payload":      {func(c *CallConfig) { c.Payload = "{" }, errs.KindInvalidInput, errs.CodeInputDecodeFailed},
		"empty socket": {func(c *CallConfig) { c.ControlSocket = "" }, errs.KindUsage, errs.CodeInvalidInput},
		"no server":    {func(c *CallConfig) { c.ControlSocket = filepath.Join(shortDir(t), "x.sock") }, errs.KindTransport, errs.CodeControlSocketFailed},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := NewCallConfig()
			cfg.Station, cfg.Action, cfg.Yes = "CS1", "Reset", true
			tc.cfg(&cfg)
			_, err := Call(ctx, &cfg, strings.NewReader(""))
			e, ok := errs.As(err)
			if !ok || e.Kind != tc.kind || e.Code != tc.code {
				t.Errorf("got %v, want %s/%s", err, tc.kind, tc.code)
			}
		})
	}
	// A read-only CALL needs no --yes; a raw unknown action passes the client checks.
	r := start(t, plainConfig())
	mustDial(t, r.url("CS1"), ocpp.V16)
	r.ev.waitEvent(t, "connected", "CS1", "")
	cfg := NewCallConfig()
	cfg.ControlSocket, cfg.Station, cfg.Action, cfg.Timeout = r.socket, "CS1", "GetConfiguration", "300ms"
	if _, err := Call(ctx, &cfg, nil); err == nil {
		t.Error("the station without handler answered")
	} else if e, _ := errs.As(err); e.Code != errs.CodeCallError {
		t.Errorf("GetConfiguration: %v", err)
	}
	cfg.Action, cfg.Raw, cfg.Yes = "Bogus", true, true
	if _, err := Call(ctx, &cfg, nil); err == nil {
		t.Error("a bogus action was answered")
	}
	sc := NewControlConfig()
	sc.ControlSocket = r.socket
	res, err := Stations(ctx, &sc)
	if err != nil || len(res.Stations) != 1 || res.Target != r.socket {
		t.Errorf("stations: %+v %v", res, err)
	}
}

func TestEventRendering(t *testing.T) {
	ms := int64(5)
	ev := types.NewServerEvent("call_result")
	ev.Identity, ev.Version, ev.Action, ev.DurationMS = "CS1", "2.0.1", "Reset", &ms
	ev.Response, ev.ErrorCode, ev.ErrorDescription, ev.Reason = json.RawMessage(`{"status": "Accepted"}`), "X", "d", "r"
	ev.Address, ev.Versions, ev.ControlSocket, ev.TLS, ev.Compression = "a", []string{"1.6"}, "s", ptr(true), ptr(false)
	ev.Request = json.RawMessage(`not json`)
	text, _ := ev.MarshalTextOutput()
	for _, want := range []string{"call_result", "CS1", "Reset", `response={"status":"Accepted"}`, "duration_ms=5", "tls=true", "request=not json"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q: %s", want, text)
		}
	}
	if rows := ev.TableRows(); len(rows[0]) != len(ev.TableHeaders()) {
		t.Error("table row width")
	}
	empty := types.StationsResult{Stations: []types.StationInfo{}}
	if s, _ := empty.MarshalTextOutput(); s != "no station connected" {
		t.Error(s)
	}
	full := types.StationsResult{Stations: []types.StationInfo{{Identity: "CS1", Version: "1.6", ConnectedAt: types.Millis(time.Now())}}}
	if s, _ := full.MarshalTextOutput(); !strings.Contains(s, "CS1") {
		t.Error(s)
	}
	if rows := full.TableRows(); len(rows) != 1 || len(rows[0]) != len(full.TableHeaders()) {
		t.Error("stations table")
	}
}

func TestUnmatchedOverride(t *testing.T) {
	// An override limited to other stations does not apply, and a 2.x-only response is not
	// checked against 1.6.
	m, err := Parse([]byte("actions:\n  - action: NotifyEvent\n    stations: [X]\n    response: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(allVersions); err != nil {
		t.Fatal(err)
	}
	if m.override(ocpp.V201, "Y", "NotifyEvent") != nil || m.override(ocpp.V201, "X", "NotifyEvent") == nil {
		t.Error("stations filter")
	}
}
