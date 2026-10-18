// SPDX-License-Identifier: MIT

package station

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

var versions = []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21}

type events struct {
	mu  sync.Mutex
	all []types.StationEvent
}

func (e *events) emit(ev types.StationEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.all = append(e.all, ev)
}

func (e *events) wait(t *testing.T, what string, ok func(types.StationEvent) bool) types.StationEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		for _, ev := range e.all {
			if ok(ev) {
				e.mu.Unlock()
				return ev
			}
		}
		e.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s event; got %+v", what, e.snapshot())
	return types.StationEvent{}
}

func (e *events) snapshot() []types.StationEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]types.StationEvent{}, e.all...)
}

func event(name string) func(types.StationEvent) bool {
	return func(ev types.StationEvent) bool { return ev.Event == name }
}

func waitCount(t *testing.T, r *ocpptest.Recorder, action string, n int) []json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.Received(action); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d %s received", n, action)
	return nil
}

func parsed(t *testing.T, url string, v ocpp.Version) *conn.Parsed {
	t.Helper()
	tg := conn.NewTarget()
	tg.URL, tg.Versions, tg.InsecureTransport = url, []string{v.String()}, true
	p, err := tg.Parse()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func start(t *testing.T, m *Model, p *conn.Parsed) (*Station, *events, func() error) {
	t.Helper()
	ev := &events{}
	s := New(Options{Parsed: p, Model: m, Reconnect: false, Emit: ev.emit, Stderr: &bytes.Buffer{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				t.Error("the station did not stop")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return s, ev, stop
}

func TestSessionLifecycle(t *testing.T) {
	for _, v := range versions {
		t.Run(v.String(), func(t *testing.T) {
			csms := testcsms.Start(t)
			m := Demo()
			m.Sessions = []Session{{After: Duration(10 * time.Millisecond), EVSE: 2, Connector: 1, IDToken: "AABBCC",
				PowerW: 36000, MeterInterval: Duration(100 * time.Millisecond), Duration: Duration(450 * time.Millisecond)}}
			s, ev, stop := start(t, m, parsed(t, csms.URL+"CS1", v))
			ev.wait(t, "connected", event("connected"))
			ev.wait(t, "booted", event("booted"))
			started := ev.wait(t, "session_started", event("session_started"))
			ended := ev.wait(t, "session_ended", event("session_ended"))
			if started.Transaction == "" || started.Transaction != ended.Transaction {
				t.Fatalf("transaction ids: %q, %q", started.Transaction, ended.Transaction)
			}
			rec := csms.Recorder(v)
			statuses := waitCount(t, rec, "StatusNotification", 4) // 2 at boot, busy, free
			if v == ocpp.V16 {
				if started.Transaction != "7" {
					t.Fatalf("1.6 transaction id %q, want the CSMS's 7", started.Transaction)
				}
				var st struct {
					ConnectorID int `json:"connectorId"`
				}
				_ = json.Unmarshal(statuses[2], &st)
				if st.ConnectorID != 2 {
					t.Fatalf("EVSE 2 connector 1 is 1.6 connector 2, got %d", st.ConnectorID)
				}
				waitCount(t, rec, "MeterValues", 1)
				waitCount(t, rec, "StopTransaction", 1)
			} else {
				txs := waitCount(t, rec, "TransactionEvent", 3)
				for i, want := range []string{"Started", "Updated"} {
					if !strings.Contains(string(txs[i]), want) {
						t.Fatalf("TransactionEvent %d: %s", i, txs[i])
					}
				}
				waitFor(t, func() bool {
					all := rec.Received("TransactionEvent")
					return strings.Contains(string(all[len(all)-1]), `"Ended"`)
				})
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			if st := s.Stats(); !st.Booted || st.Calls < 5 || st.Sessions != 1 || st.Errors != 0 {
				t.Fatalf("stats %+v", st)
			}
		})
	}
}

func countEvents(e *events, name string) int {
	n := 0
	for _, ev := range e.snapshot() {
		if ev.Event == name {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func call(t *testing.T, sess interface {
	Call(context.Context, string, any, any) error
}, action, req string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp map[string]any
	if err := sess.Call(ctx, action, json.RawMessage(req), &resp); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return resp
}

func callErr(t *testing.T, sess interface {
	Call(context.Context, string, any, any) error
}, action, req string) *ocpp.CallError {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp json.RawMessage
	err := sess.Call(ctx, action, json.RawMessage(req), &resp)
	ce, ok := err.(*ocpp.CallError)
	if !ok {
		t.Fatalf("%s: want a CALLERROR, got %v (%s)", action, err, resp)
	}
	return ce
}

func TestAnswers16(t *testing.T) {
	csms := testcsms.Start(t)
	m := Demo()
	m.Answers["UpdateFirmware"] = Answer{Behaviour: Reject}
	_, ev, _ := start(t, m, parsed(t, csms.URL+"CS16", ocpp.V16))
	ev.wait(t, "booted", event("booted"))
	sess := ocpptest.Session(t, csms.Server, "CS16")
	rec := csms.Recorder(ocpp.V16)

	got := call(t, sess, "GetConfiguration", `{"key":["HeartbeatInterval","Nope"]}`)
	if keys := got["configurationKey"].([]any); len(keys) != 1 || got["unknownKey"].([]any)[0] != "Nope" {
		t.Fatalf("GetConfiguration: %v", got)
	}
	if all := call(t, sess, "GetConfiguration", `{}`); len(all["configurationKey"].([]any)) != 4 {
		t.Fatalf("GetConfiguration all: %v", all)
	}
	for req, want := range map[string]string{
		`{"key":"HeartbeatInterval","value":"5"}`:  "Accepted",
		`{"key":"NumberOfConnectors","value":"9"}`: "Rejected",
		`{"key":"Bogus","value":"1"}`:              "NotSupported",
	} {
		if got := call(t, sess, "ChangeConfiguration", req)["status"]; got != want {
			t.Fatalf("ChangeConfiguration %s: %v, want %s", req, got, want)
		}
	}
	if got := call(t, sess, "UnlockConnector", `{"connectorId":1}`)["status"]; got != "Unlocked" {
		t.Fatalf("UnlockConnector: %v", got)
	}
	if ce := callErr(t, sess, "UpdateFirmware", `{"location":"https://x/fw.bin","retrieveDate":"2026-01-01T00:00:00Z"}`); ce.Code != ocpp.NotSupported {
		t.Fatalf("UpdateFirmware reject: %v", ce)
	}
	if got := call(t, sess, "GetLocalListVersion", `{}`)["listVersion"]; got != float64(-1) {
		t.Fatalf("GetLocalListVersion: %v", got)
	}
	if got := call(t, sess, "ClearCache", `{}`)["status"]; got != "Accepted" {
		t.Fatalf("ClearCache default: %v", got)
	}
	if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"Heartbeat"}`)["status"]; got != "Accepted" {
		t.Fatalf("TriggerMessage: %v", got)
	}
	waitCount(t, rec, "Heartbeat", 1)
	if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"DiagnosticsStatusNotification"}`)["status"]; got != "Accepted" {
		t.Fatalf("TriggerMessage diagnostics: %v", got)
	}
	waitCount(t, rec, "DiagnosticsStatusNotification", 1)
	if got := call(t, sess, "RemoteStartTransaction", `{"idTag":"TAG1","connectorId":9}`)["status"]; got != "Rejected" {
		t.Fatalf("RemoteStart on an unknown connector: %v", got)
	}
	if got := call(t, sess, "RemoteStartTransaction", `{"idTag":"TAG1","connectorId":1}`)["status"]; got != "Accepted" {
		t.Fatalf("RemoteStart: %v", got)
	}
	ev.wait(t, "session_started", event("session_started"))
	if got := call(t, sess, "RemoteStartTransaction", `{"idTag":"TAG1","connectorId":1}`)["status"]; got != "Rejected" {
		t.Fatalf("RemoteStart on a busy connector: %v", got)
	}
	if got := call(t, sess, "RemoteStopTransaction", `{"transactionId":99}`)["status"]; got != "Rejected" {
		t.Fatalf("RemoteStop of an unknown transaction: %v", got)
	}
	if got := call(t, sess, "RemoteStopTransaction", `{"transactionId":7}`)["status"]; got != "Accepted" {
		t.Fatalf("RemoteStop: %v", got)
	}
	if stop := waitCount(t, rec, "StopTransaction", 1); !strings.Contains(string(stop[0]), `"reason":"Remote"`) {
		t.Fatalf("StopTransaction: %s", stop[0])
	}
	if got := call(t, sess, "RemoteStartTransaction", `{"idTag":"TAG2","connectorId":2}`)["status"]; got != "Accepted" {
		t.Fatalf("RemoteStart: %v", got)
	}
	waitCount(t, rec, "StartTransaction", 2)
	if ce := callErr(t, sess, "ChangeConfiguration", `{"key":1}`); ce.Code == "" {
		t.Fatalf("invalid request: %v", ce)
	}
	if got := call(t, sess, "Reset", `{"type":"Soft"}`)["status"]; got != "Accepted" {
		t.Fatalf("Reset: %v", got)
	}
	ev.wait(t, "reset", event("reset"))
	if stops := waitCount(t, rec, "StopTransaction", 2); !strings.Contains(string(stops[1]), `"reason":"SoftReset"`) {
		t.Fatalf("a Soft Reset ends the session with SoftReset: %s", stops[1])
	}
	waitCount(t, rec, "BootNotification", 2)
	// the interval the CSMS gave (300 s) is what the configuration reports
	sess = ocpptest.Session(t, csms.Server, "CS16")
	waitFor(t, func() bool { return countEvents(ev, "booted") == 2 })
	got = call(t, sess, "GetConfiguration", `{"key":["HeartbeatInterval"]}`)
	if v := got["configurationKey"].([]any)[0].(map[string]any)["value"]; v != "300" {
		t.Fatalf("HeartbeatInterval %v", v)
	}
}

func TestAnswers2x(t *testing.T) {
	for _, v := range []ocpp.Version{ocpp.V201, ocpp.V21} {
		t.Run(v.String(), func(t *testing.T) {
			csms := testcsms.Start(t)
			_, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CS2", v))
			ev.wait(t, "booted", event("booted"))
			sess := ocpptest.Session(t, csms.Server, "CS2")
			rec := csms.Recorder(v)

			got := call(t, sess, "GetVariables", `{"getVariableData":[
				{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}},
				{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"Nope"}},
				{"component":{"name":"Nope"},"variable":{"name":"X"}},
				{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"},"attributeType":"Target"}]}`)
			var statuses []string
			for _, r := range got["getVariableResult"].([]any) {
				statuses = append(statuses, r.(map[string]any)["attributeStatus"].(string))
			}
			if strings.Join(statuses, ",") != "Accepted,UnknownVariable,UnknownComponent,NotSupportedAttributeType" {
				t.Fatalf("GetVariables: %v", statuses)
			}
			got = call(t, sess, "SetVariables", `{"setVariableData":[
				{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"},"attributeValue":"5"},
				{"component":{"name":"SecurityCtrlr"},"variable":{"name":"SecurityProfile"},"attributeValue":"3"}]}`)
			statuses = nil
			for _, r := range got["setVariableResult"].([]any) {
				statuses = append(statuses, r.(map[string]any)["attributeStatus"].(string))
			}
			if strings.Join(statuses, ",") != "Accepted,Rejected" {
				t.Fatalf("SetVariables: %v", statuses)
			}
			if got := call(t, sess, "GetBaseReport", `{"requestId":3,"reportBase":"FullInventory"}`)["status"]; got != "Accepted" {
				t.Fatalf("GetBaseReport: %v", got)
			}
			if r := waitCount(t, rec, "NotifyReport", 1); !strings.Contains(string(r[0]), `"requestId":3`) {
				t.Fatalf("NotifyReport: %s", r[0])
			}
			if got := call(t, sess, "GetLocalListVersion", `{}`)["versionNumber"]; got != float64(0) {
				t.Fatalf("GetLocalListVersion: %v", got)
			}
			if got := call(t, sess, "GetTransactionStatus", `{"transactionId":"none"}`); got["ongoingIndicator"] != false || got["messagesInQueue"] != false {
				t.Fatalf("GetTransactionStatus: %v", got)
			}
			if got := call(t, sess, "UnpublishFirmware", `{"checksum":"0123456789abcdef0123456789abcdef"}`)["status"]; got != "NoFirmware" {
				t.Fatalf("UnpublishFirmware: %v", got)
			}
			if got := call(t, sess, "ClearVariableMonitoring", `{"id":[4,5]}`)["clearMonitoringResult"].([]any); len(got) != 2 {
				t.Fatalf("ClearVariableMonitoring: %v", got)
			}
			got = call(t, sess, "SetVariableMonitoring", `{"setMonitoringData":[
				{"value":1,"type":"Delta","severity":5,"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}},
				{"value":1,"type":"Delta","severity":5,"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"Nope"}},
				{"value":1,"type":"Delta","severity":5,"component":{"name":"Nope"},"variable":{"name":"X"}}]}`)
			statuses = nil
			for _, r := range got["setMonitoringResult"].([]any) {
				statuses = append(statuses, r.(map[string]any)["status"].(string))
			}
			if strings.Join(statuses, ",") != "Rejected,UnknownVariable,UnknownComponent" {
				t.Fatalf("SetVariableMonitoring: %v", statuses)
			}
			if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"StatusNotification"}`)["status"]; got != "Accepted" {
				t.Fatalf("TriggerMessage: %v", got)
			}
			if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"SignChargingStationCertificate"}`)["status"]; got != "NotImplemented" {
				t.Fatalf("TriggerMessage of a message the simulator does not send: %v", got)
			}
			start := call(t, sess, "RequestStartTransaction", `{"evseId":1,"remoteStartId":12,"idToken":{"idToken":"T1","type":"ISO14443"}}`)
			if start["status"] != "Accepted" || start["transactionId"] == nil {
				t.Fatalf("RequestStartTransaction: %v", start)
			}
			waitFor(t, func() bool { return len(rec.Received("TransactionEvent")) > 0 })
			if te := rec.Received("TransactionEvent")[0]; !strings.Contains(string(te), `"remoteStartId":12`) || !strings.Contains(string(te), `"RemoteStart"`) {
				t.Fatalf("TransactionEvent Started: %s", te)
			}
			if got := call(t, sess, "RequestStopTransaction", `{"transactionId":"nope"}`)["status"]; got != "Rejected" {
				t.Fatalf("RequestStop of an unknown transaction: %v", got)
			}
			if got := call(t, sess, "RequestStopTransaction", `{"transactionId":"`+start["transactionId"].(string)+`"}`)["status"]; got != "Accepted" {
				t.Fatalf("RequestStop: %v", got)
			}
			ev.wait(t, "session_ended", event("session_ended"))
			// OnIdle while charging is Scheduled, and carried out when the session ends
			idle := call(t, sess, "RequestStartTransaction", `{"evseId":2,"remoteStartId":13,"idToken":{"idToken":"T2","type":"ISO14443"}}`)
			waitFor(t, func() bool { return len(rec.Received("TransactionEvent")) >= 3 })
			if got := call(t, sess, "Reset", `{"type":"OnIdle"}`)["status"]; got != "Scheduled" {
				t.Fatalf("Reset OnIdle while charging: %v", got)
			}
			if got := call(t, sess, "RequestStopTransaction", `{"transactionId":"`+idle["transactionId"].(string)+`"}`)["status"]; got != "Accepted" {
				t.Fatalf("RequestStop: %v", got)
			}
			waitCount(t, rec, "BootNotification", 2)
			sess = ocpptest.Session(t, csms.Server, "CS2")
			waitFor(t, func() bool { return countEvents(ev, "booted") == 2 })
			if got := call(t, sess, "Reset", `{"type":"Immediate","evseId":1}`)["status"]; got != "Rejected" {
				t.Fatalf("Reset of an EVSE: %v", got)
			}
			if got := call(t, sess, "Reset", `{"type":"Immediate"}`)["status"]; got != "Accepted" {
				t.Fatalf("Reset: %v", got)
			}
			boots := waitCount(t, rec, "BootNotification", 3)
			if !strings.Contains(string(boots[2]), "RemoteReset") {
				t.Fatalf("second boot: %s", boots[1])
			}
		})
	}
}

func TestBootPendingAndLostConnection(t *testing.T) {
	csms := testcsms.Start(t)
	csms.Recorder(ocpp.V201).Answer("BootNotification", `{"status":"Pending","currentTime":"2026-01-01T00:00:00Z","interval":1}`)
	_, ev, stop := start(t, Demo(), parsed(t, csms.URL+"CSP", ocpp.V201))
	ev.wait(t, "boot_pending", event("boot_pending"))
	sess := ocpptest.Session(t, csms.Server, "CSP")
	csms.Recorder(ocpp.V201).Answer("BootNotification", `{"status":"Accepted","currentTime":"2026-01-01T00:00:00Z","interval":1}`)
	if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"BootNotification"}`)["status"]; got != "Accepted" {
		t.Fatalf("TriggerMessage: %v", got)
	}
	ev.wait(t, "booted", event("booted"))
	waitCount(t, csms.Recorder(ocpp.V201), "Heartbeat", 1) // interval 1s
	_ = sess.Close()
	ev.wait(t, "disconnected", event("disconnected"))
	if err := stop(); err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("a lost connection without reconnect: %v", err)
	}
}

func TestReconnect(t *testing.T) {
	csms := testcsms.Start(t)
	ev := &events{}
	s := New(Options{Parsed: parsed(t, csms.URL+"CSR", ocpp.V16), Model: Demo(), Reconnect: true,
		Backoff: client.Reconnect{WaitMinimum: 50 * time.Millisecond}, Emit: ev.emit})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	ev.wait(t, "booted", event("booted"))
	_ = ocpptest.Session(t, csms.Server, "CSR").Close()
	ev.wait(t, "reconnecting", event("reconnecting"))
	ev.wait(t, "reconnected", func(e types.StationEvent) bool { return e.Event == "connected" && e.Message == "reconnected" })
	waitCount(t, csms.Recorder(ocpp.V16), "StatusNotification", 4) // the connectors again
	if n := len(csms.Recorder(ocpp.V16).Received("BootNotification")); n != 1 {
		t.Fatalf("a reconnection is not a reboot: %d BootNotifications", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
