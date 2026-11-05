// SPDX-License-Identifier: MIT

package station

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

// An identity range whose numbers do not fit an int is refused: it used to pass the size
// check through an overflow, and then panicked (makeslice) or looped for ever.
func TestExpandURLOverflow(t *testing.T) {
	for _, u := range []string{
		"ws://h/CS{0..99999999999999999999}",
		"ws://h/CS{99999999999999999999..99999999999999999999}",
		"ws://h/CS{1..9223372036854775807}",
	} {
		done := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- nil
					t.Errorf("%s: panic %v", u, r)
				}
			}()
			_, err := ExpandURL(u)
			done <- err
		}()
		select {
		case err := <-done:
			if e, ok := errs.As(err); !ok || e.Code != errs.CodeInvalidURL {
				t.Errorf("%s: %v, want invalid_url", u, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: ExpandURL does not return", u)
		}
	}
	// The largest int is a valid identity number: the loop that numbers the stations used
	// to overflow past it and never end.
	maxInt := strconv.Itoa(math.MaxInt)
	if got, err := ExpandURL("ws://h/CS{" + maxInt + ".." + maxInt + "}"); err != nil || len(got) != 1 || got[0] != "ws://h/CS"+maxInt {
		t.Fatalf("one station at the largest int: %v, %v", got, err)
	}
	// The largest range still works, and numbers keep their width.
	got, err := ExpandURL("ws://h/CS{00001..10000}")
	if err != nil || len(got) != MaxStations || got[0] != "ws://h/CS00001" || got[MaxStations-1] != "ws://h/CS10000" {
		t.Fatalf("largest range: %d urls, %v", len(got), err)
	}
}

func TestSeconds(t *testing.T) {
	cases := map[int]time.Duration{1: time.Second, 300: 300 * time.Second, math.MaxInt: maxInterval}
	if strconv.IntSize == 64 {
		var wraps, negative int64 = 18446744074, 9223372037
		cases[int(wraps)] = maxInterval    // n*1e9 wraps to 290 ms
		cases[int(negative)] = maxInterval // n*1e9 wraps negative
	}
	for n, want := range cases {
		if got := seconds(n); got != want || got <= 0 {
			t.Errorf("seconds(%d) = %s, want %s", n, got, want)
		}
	}
}

// An interval the CSMS sets that does not fit a time.Duration does not turn into a
// heartbeat every few hundred milliseconds.
func TestHugeHeartbeatInterval(t *testing.T) {
	const huge = "18446744074" // seconds; times 1e9 it wraps to 290 ms
	if strconv.IntSize < 64 {
		t.Skip("the interval does not fit an int of this platform, so it cannot overflow a duration")
	}
	t.Run("BootNotification", func(t *testing.T) {
		csms := testcsms.Start(t)
		csms.Recorder(ocpp.V201).Answer("BootNotification", `{"status":"Accepted","currentTime":"2026-01-01T00:00:00Z","interval":`+huge+`}`)
		s, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CSH1", ocpp.V201))
		ev.wait(t, "booted", event("booted"))
		if d := s.heartbeatInterval(); d < time.Hour {
			t.Fatalf("heartbeat every %s", d)
		}
	})
	t.Run("SetVariables", func(t *testing.T) {
		csms := testcsms.Start(t)
		s, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CSH2", ocpp.V201))
		ev.wait(t, "booted", event("booted"))
		sess := ocpptest.Session(t, csms.Server, "CSH2")
		call(t, sess, "SetVariables", `{"setVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"},"attributeValue":"`+huge+`"}]}`)
		if d := s.heartbeatInterval(); d < time.Hour {
			t.Fatalf("heartbeat every %s", d)
		}
	})
	t.Run("ChangeConfiguration", func(t *testing.T) {
		csms := testcsms.Start(t)
		s, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CSH3", ocpp.V16))
		ev.wait(t, "booted", event("booted"))
		sess := ocpptest.Session(t, csms.Server, "CSH3")
		call(t, sess, "ChangeConfiguration", `{"key":"HeartbeatInterval","value":"`+huge+`"}`)
		if d := s.heartbeatInterval(); d < time.Hour {
			t.Fatalf("heartbeat every %s", d)
		}
	})
}

// A BootNotification answered Pending is sent again after the interval the CSMS gave,
// also when the CSMS triggers other messages meanwhile: running them must not cut the
// wait short.
func TestBootPendingWaitsThroughTriggers(t *testing.T) {
	csms := testcsms.Start(t)
	rec := csms.Recorder(ocpp.V201)
	rec.Answer("BootNotification", `{"status":"Pending","currentTime":"2026-01-01T00:00:00Z","interval":3600}`)
	_, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CSPW", ocpp.V201))
	ev.wait(t, "boot_pending", event("boot_pending"))
	sess := ocpptest.Session(t, csms.Server, "CSPW")
	for range 3 {
		if got := call(t, sess, "TriggerMessage", `{"requestedMessage":"Heartbeat"}`)["status"]; got != "Accepted" {
			t.Fatalf("TriggerMessage: %v", got)
		}
	}
	waitCount(t, rec, "Heartbeat", 3)
	if n := len(rec.Received("BootNotification")); n != 1 {
		t.Fatalf("%d BootNotifications while Pending with an interval of an hour, want 1", n)
	}
	if ev := countEvents(ev, "boot_pending"); ev != 1 {
		t.Fatalf("%d boot_pending events", ev)
	}
}

// A negative remote_power_w is refused like a negative power_w: the energy register of a
// remotely started session would run backwards.
func TestNegativeRemotePower(t *testing.T) {
	m := Demo()
	m.RemotePowerW = -1
	if err := m.Validate(versions); err == nil || !strings.Contains(err.Error(), "remote_power_w") {
		t.Fatalf("remote_power_w -1: %v", err)
	}
}

// Power that is not a finite number is refused: the meter values would not be JSON.
func TestNonFinitePower(t *testing.T) {
	for _, doc := range []string{"remote_power_w: .nan\n", "remote_power_w: .inf\n",
		"sessions: [{after: 1s, evse: 1, connector: 1, id_token: A, power_w: .nan, duration: 1m}]\n",
		"sessions: [{after: 1s, evse: 1, connector: 1, id_token: A, power_w: .inf, duration: 1m}]\n"} {
		m, err := Parse([]byte("station: {vendor: v, model: m}\nevses: [{id: 1, connectors: [1]}]\n" + doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Validate(versions); err == nil {
			t.Errorf("%s: accepted", strings.TrimSpace(doc))
		}
	}
}

// The model's answers: each behaviour, and a static answer that does not fit the
// version, which falls through to the built-in answer.
func TestModelAnswers(t *testing.T) {
	csms := testcsms.Start(t)
	m := Demo()
	m.Answers = map[string]Answer{
		"ClearCache":          {Behaviour: Accept},
		"ChangeAvailability":  {Behaviour: Reject},
		"UnlockConnector":     {Behaviour: NotSupported},
		"ReserveNow":          {Behaviour: NotImplemented},
		"CancelReservation":   {Behaviour: Reject},
		"GetLocalListVersion": {Static: json.RawMessage(`{"listVersion":5}`)}, // 1.6 only
		"DataTransfer":        {Static: json.RawMessage(`{"status":"UnknownVendorId"}`)},
		"SetChargingProfile":  {Behaviour: "bogus"},
	}
	_, ev, _ := start(t, m, parsed(t, csms.URL+"CSMA", ocpp.V201))
	ev.wait(t, "booted", event("booted"))
	sess := ocpptest.Session(t, csms.Server, "CSMA")
	if got := call(t, sess, "ClearCache", `{}`)["status"]; got != "Accepted" {
		t.Errorf("accept: %v", got)
	}
	if got := call(t, sess, "ChangeAvailability", `{"operationalStatus":"Inoperative"}`)["status"]; got != "Rejected" {
		t.Errorf("reject: %v", got)
	}
	if ce := callErr(t, sess, "UnlockConnector", `{"evseId":1,"connectorId":1}`); ce.Code != ocpp.NotSupported {
		t.Errorf("not-supported: %v", ce)
	}
	if ce := callErr(t, sess, "ReserveNow", `{"id":1,"expiryDateTime":"2026-01-01T00:00:00Z","idToken":{"idToken":"A","type":"ISO14443"}}`); ce.Code != ocpp.NotImplemented {
		t.Errorf("not-implemented: %v", ce)
	}
	if got := call(t, sess, "CancelReservation", `{"reservationId":1}`)["status"]; got != "Rejected" {
		t.Errorf("reject with a status: %v", got)
	}
	if got := call(t, sess, "GetLocalListVersion", `{}`)["versionNumber"]; got != float64(0) {
		t.Errorf("a 1.6 static answer on 2.0.1 falls through to the built-in one: %v", got)
	}
	if got := call(t, sess, "DataTransfer", `{"vendorId":"x"}`)["status"]; got != "UnknownVendorId" {
		t.Errorf("static: %v", got)
	}
	if got := call(t, sess, "SetChargingProfile", `{"evseId":1,"chargingProfile":{"id":1,"stackLevel":0,"chargingProfilePurpose":"TxDefaultProfile","chargingProfileKind":"Absolute","chargingSchedule":[{"id":1,"chargingRateUnit":"W","chargingSchedulePeriod":[{"startPeriod":0,"limit":1}]}]}}`)["status"]; got != "Accepted" {
		t.Errorf("an unknown behaviour falls through to the default: %v", got)
	}
	answered := 0
	for _, e := range ev.snapshot() {
		if e.Event == "answered" && (e.ErrorCode == "NotSupported" || e.ErrorCode == "NotImplemented") {
			answered++
		}
	}
	if answered != 2 {
		t.Errorf("%d answered events with a CALLERROR, want 2", answered)
	}
	// A reject of an action whose response has no status Rejected is a NotSupported.
	d := &dispatcher{s: New(Options{Parsed: parsed(t, "ws://h/CSX", ocpp.V16), Model: Demo(), Emit: func(types.StationEvent) {}}), v: ocpp.V16}
	if _, err, ok := d.fromModel("ClearChargingProfile", Answer{Behaviour: Reject}); !ok || err == nil {
		t.Errorf("reject without a Rejected status: %v, %v", err, ok)
	}
	if err := d.DispatchSend(context.Background(), nil, "X", nil); err != nil {
		t.Errorf("DispatchSend: %v", err)
	}
}

// What the CSMS does wrong shows up as a call_error event with the code: a CALLERROR,
// a response that does not conform to the schema.
func TestCallErrors(t *testing.T) {
	csms := testcsms.Start(t)
	rec := csms.Recorder(ocpp.V16)
	rec.AnswerError("StatusNotification", ocpp.NewCallError(ocpp.InternalError, "\x1b[31mboom"))
	rec.Answer("Heartbeat", `{"currentTime":"not a time"}`)
	_, ev, _ := start(t, Demo(), parsed(t, csms.URL+"CSCE", ocpp.V16))
	ev.wait(t, "booted", event("booted"))
	e := ev.wait(t, "call_error", func(e types.StationEvent) bool { return e.Event == "call_error" && e.Action == "StatusNotification" })
	if e.ErrorCode != "InternalError" || e.DurationMS == nil {
		t.Errorf("CALLERROR: %+v", e)
	}
	sess := ocpptest.Session(t, csms.Server, "CSCE")
	call(t, sess, "TriggerMessage", `{"requestedMessage":"Heartbeat"}`)
	e = ev.wait(t, "invalid response", func(e types.StationEvent) bool { return e.Event == "call_error" && e.Action == "Heartbeat" })
	if e.ErrorCode != errs.CodeInvalidResponse {
		t.Errorf("invalid response: %+v", e)
	}
	call(t, sess, "TriggerMessage", `{"requestedMessage":"MeterValues"}`)
	waitCount(t, rec, "MeterValues", 1)
	call(t, sess, "TriggerMessage", `{"requestedMessage":"FirmwareStatusNotification"}`)
	waitCount(t, rec, "FirmwareStatusNotification", 1)
	if countEvents(ev, "call_sent") == 0 {
		t.Error("no call_sent events")
	}
}

// A full work queue drops the task and says so instead of blocking the dispatcher.
func TestEnqueueFull(t *testing.T) {
	ev := &events{}
	s := New(Options{Parsed: parsed(t, "ws://h/CSQ", ocpp.V16), Model: Demo(), Emit: ev.emit})
	for range cap(s.tasks) + 1 {
		s.enqueue(func(context.Context) {})
	}
	if countEvents(ev, "error") != 1 {
		t.Fatalf("events: %+v", ev.snapshot())
	}
	x := &session{evse: 1, connector: 1}
	x.timers = append(x.timers, time.AfterFunc(time.Hour, func() {}))
	s.sessions[x.key()] = x
	s.stopTimers()
	if x.timers[0].Stop() {
		t.Fatal("stopTimers left a timer running")
	}
	if lower("") != "unknown" || lower("Pending") != "pending" || lower("x") != "x" {
		t.Fatal("lower")
	}
	if c, v, ok := cut("NoDot"); c != "NoDot" || v != "" || ok {
		t.Fatal("cut")
	}
}

func TestModelEncoding(t *testing.T) {
	m := Demo()
	m.Answers["X"] = Answer{Static: json.RawMessage(`{"a":1}`)}
	b, err := m.Encode()
	if err != nil || !strings.Contains(string(b), `"UnlockConnector": {`) || !strings.Contains(string(b), `"X": {`) {
		t.Fatalf("Encode: %s, %v", b, err)
	}
	if y, err := (Answer{Behaviour: Reject}).MarshalYAML(); err != nil || y != Reject {
		t.Fatalf("MarshalYAML: %v, %v", y, err)
	}
	if j, err := (Answer{Behaviour: Accept}).MarshalJSON(); err != nil || string(j) != `"accept"` {
		t.Fatalf("MarshalJSON: %s, %v", j, err)
	}
	for _, doc := range []string{
		"answers: {Reset: [a]}",
		"answers: {Reset: {a: [1, {b: c}]}}",
		"heartbeat_interval: [1]",
	} {
		m, err := Parse([]byte(doc))
		if strings.HasPrefix(doc, "answers: {Reset: {") {
			if err != nil || m.Answers["Reset"].Static == nil {
				t.Errorf("%s: %v", doc, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", doc)
		}
	}
	bad := Demo()
	bad.Station.Vendor = strings.Repeat("v", 21)
	bad.EVSEs = append(bad.EVSEs, EVSE{ID: 1}, EVSE{ID: 0, Connectors: []int{0, 0}})
	bad.Heartbeat = Duration(time.Millisecond)
	bad.MeterInterval = Duration(-time.Second)
	bad.Sessions = append(bad.Sessions, Session{EVSE: 9, Connector: 9, After: Duration(-1), MeterInterval: Duration(time.Millisecond)})
	bad.DeviceModel["NoDot"] = Variable{Mutability: "Sometimes"}
	bad.Answers["BootNotification"] = Answer{Behaviour: Accept}
	bad.Answers["Nope"] = Answer{Behaviour: Accept}
	bad.Answers["Reset"] = Answer{Static: json.RawMessage(`{"status":"Maybe"}`)}
	err = bad.Validate([]ocpp.Version{ocpp.V16})
	if err == nil {
		t.Fatal("a broken model validated")
	}
	for _, want := range []string{"vendor is at most 20", "id 1 is used twice", "an EVSE needs at least one connector", "EVSE ids start at 1",
		"connector 0", "heartbeat_interval", "meter_interval -1s", "EVSE 9 has no connector 9", "id_token is required", "duration must be positive",
		"sessions[1]: meter_interval", "Component.Variable", "mutability", "sent by the station", "not an action a CSMS sends", "Reset: the response is not valid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the problems do not say %q:\n%s", want, err)
		}
	}
	if (&Model{}).Validate(versions) == nil {
		t.Fatal("an empty model validated")
	}
}
