// SPDX-License-Identifier: MIT

package station

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/payload"
	"github.com/otfabric/ocppctl/internal/types"
)

// Options configure one simulated station.
type Options struct {
	Parsed    *conn.Parsed
	Model     *Model
	Reconnect bool
	// Backoff is the reconnection back-off; zero is go-ocpp's default (OCPP 2.x §5.4).
	Backoff client.Reconnect
	// RestartDelay is how long the station is gone after a Reset before it
	// connects again, as a charger that restarts (default 2s).
	RestartDelay time.Duration
	PingInterval time.Duration
	Debug        bool
	Stderr       io.Writer
	// Emit receives every event; it must be safe for concurrent use.
	Emit func(types.StationEvent)
}

// Stats are what a station did, for the summary of `client run`.
type Stats struct {
	Identity string
	Booted   bool
	Calls    int
	Errors   int
	Total    time.Duration
	Max      time.Duration
	Sessions int
}

// Station is one simulated Charging Station.
type Station struct {
	o     Options
	model *Model
	id    string

	mu        sync.Mutex
	vars      map[string]Variable
	conf      map[string]ConfigKey
	sessions  map[string]*session
	heartbeat time.Duration
	c         *conn.Conn
	v         ocpp.Version
	scheduled bool
	up        bool
	// resetReason is the stop reason of the sessions a Reset ends; resetOnIdle defers a
	// 2.x OnIdle Reset until the last session ends.
	resetReason string
	resetOnIdle bool
	stats       Stats

	tasks     chan func(context.Context)
	connected chan struct{}
	lost      chan error
	hbChanged chan struct{}
	resetCh   chan struct{}
}

type session struct {
	evse, connector  int
	token, tokenType string
	power            float64
	interval         time.Duration
	duration         time.Duration
	remote           bool
	remoteStartID    int
	tx               string
	energy           float64
	seq              int
	last             time.Time
	timers           []*time.Timer
}

func (x *session) key() string { return strconv.Itoa(x.evse) + "/" + strconv.Itoa(x.connector) }

// New returns a station; Run connects it.
func New(o Options) *Station {
	s := &Station{
		o: o, model: o.Model, id: o.Parsed.Identity,
		vars: map[string]Variable{}, conf: map[string]ConfigKey{}, sessions: map[string]*session{},
		heartbeat: o.Model.Heartbeat.D(),
		tasks:     make(chan func(context.Context), 256),
		connected: make(chan struct{}, 1),
		lost:      make(chan error, 1),
		hbChanged: make(chan struct{}, 1),
		resetCh:   make(chan struct{}, 1),
	}
	for k, v := range o.Model.DeviceModel {
		s.vars[k] = v
	}
	for k, v := range o.Model.Configuration {
		s.conf[k] = v
	}
	s.stats.Identity = s.id
	return s
}

// Stats returns what the station did so far.
func (s *Station) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Station) emit(e types.StationEvent) {
	if e.Time.IsZero() {
		e.Time = types.Millis(time.Now())
	}
	e.Station = s.id
	s.o.Emit(e)
}

func (s *Station) enqueue(t func(context.Context)) {
	select {
	case s.tasks <- t:
	default:
		s.emit(types.StationEvent{Event: "error", Message: "the station's work queue is full: a task was dropped"})
	}
}

func (s *Station) requestReset() {
	select {
	case s.resetCh <- struct{}{}:
	default:
	}
}

func (s *Station) setHeartbeat(d time.Duration) {
	s.mu.Lock()
	s.heartbeat = d
	s.mu.Unlock()
	select {
	case s.hbChanged <- struct{}{}:
	default:
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Run connects the station and runs it until ctx ends. The first connection must succeed;
// later losses are reconnected when Reconnect is set, and end Run with an error otherwise.
// A Reset from the CSMS closes the connection and boots again with reason RemoteReset.
func (s *Station) Run(ctx context.Context) error {
	reason := "PowerUp"
	for first := true; ; first = false {
		c, err := s.dial(ctx, first)
		if ctx.Err() != nil {
			// Interrupted or --duration over while connecting: a normal end, not a failure.
			if c != nil {
				_ = c.Close()
			}
			return nil //nolint:nilerr // the dial error is the interruption itself
		}
		if err != nil || c == nil {
			return err
		}
		reset, err := s.serve(ctx, c, reason)
		s.stopTimers()
		_ = c.Stop() // writes the answer to a Reset before the restart
		if err != nil || !reset {
			return err
		}
		reason = "RemoteReset"
		delay := s.o.RestartDelay
		if delay == 0 {
			delay = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (s *Station) dial(ctx context.Context, first bool) (*conn.Conn, error) {
	extra := []client.Option{client.WithStateHandler(s.onState)}
	if s.o.Reconnect {
		b := s.o.Backoff
		if b.WaitMinimum == 0 {
			b = client.DefaultReconnect()
		}
		extra = append(extra, client.WithReconnect(b))
	}
	if s.o.PingInterval > 0 {
		extra = append(extra, client.WithPingInterval(s.o.PingInterval))
	}
	opts := conn.Options{
		Handler: func(v ocpp.Version) ocpp.Dispatcher { return &dispatcher{s: s, v: v} },
		Debug:   s.o.Debug, Stderr: s.o.Stderr, Extra: extra,
	}
	delay := time.Second
	for {
		c, err := conn.Dial(ctx, s.o.Parsed, opts)
		if err == nil {
			s.mu.Lock()
			s.c, s.v = c, c.Version()
			s.mu.Unlock()
			h := c.Handshake
			s.emit(types.StationEvent{Event: "connected", Version: c.Version().String(),
				Message: fmt.Sprintf("tls=%t compression=%t", h.TLS != nil, h.Compressed())})
			return c, nil
		}
		if first || !s.o.Reconnect || ctx.Err() != nil {
			return nil, err
		}
		s.emit(types.StationEvent{Event: "reconnecting", Message: err.Error()})
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(delay):
		}
		delay = min(2*delay, time.Minute)
	}
}

// onState is the client's state handler: it must not block.
func (s *Station) onState(st ocpp.State, err error) {
	switch st {
	case ocpp.Connected:
		s.mu.Lock()
		s.up = true
		s.mu.Unlock()
		signal(s.connected)
	case ocpp.Disconnected:
		s.mu.Lock()
		up := s.up
		s.up = false
		s.mu.Unlock()
		if err == nil || !up {
			return
		}
		s.emit(types.StationEvent{Event: "disconnected", Message: err.Error()})
		if !s.o.Reconnect {
			select {
			case s.lost <- err:
			default:
			}
		} else {
			s.emit(types.StationEvent{Event: "reconnecting"})
		}
	default:
	}
}

func (s *Station) serve(ctx context.Context, c *conn.Conn, reason string) (reset bool, err error) {
	hb := time.NewTimer(time.Hour)
	hb.Stop()
	defer hb.Stop()
	booted := false
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case err := <-s.lost:
			return false, ocpperr.Wrap(fmt.Errorf("%w: %w", ocpp.ErrConnectionLost, err), "", false)
		case <-s.connected:
			if booted {
				// A reconnection, not a reboot: the station stays booted (OCPP 2.x B01 boots
				// after a power up or reset), reports its connectors and goes on.
				s.emit(types.StationEvent{Event: "connected", Version: s.version().String(), Message: "reconnected"})
				s.statusAll(ctx)
				continue
			}
			if !s.boot(ctx, reason) {
				continue
			}
			booted = true
			reason = "PowerUp"
			hb.Reset(s.heartbeatInterval())
			s.statusAll(ctx)
			s.scheduleSessions()
		case <-hb.C:
			_, _ = s.call(ctx, "Heartbeat", empty)
			hb.Reset(s.heartbeatInterval())
		case <-s.hbChanged:
			if booted {
				hb.Reset(s.heartbeatInterval())
			}
		case t := <-s.tasks:
			t(ctx)
		case <-s.resetCh:
			s.emit(types.StationEvent{Event: "reset", Message: "Reset by the CSMS: ending sessions and rebooting"})
			s.endAll(ctx)
			return true, nil
		}
	}
}

func (s *Station) heartbeatInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeat <= 0 {
		return 300 * time.Second
	}
	return s.heartbeat
}

func (s *Station) version() ocpp.Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v
}

// call sends a CALL and reports it; the response is validated.
func (s *Station) call(ctx context.Context, action string, req json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	c, v := s.c, s.v
	s.mu.Unlock()
	if err := ocppx.ValidateRequest(v, action, req); err != nil {
		s.emit(types.StationEvent{Event: "error", Action: action, Message: "the simulator built an invalid request: " + err.Error()})
		return nil, err
	}
	s.emit(types.StationEvent{Event: "call_sent", Version: v.String(), Action: action, Request: req})
	start := time.Now()
	var resp json.RawMessage
	err := c.Call(ctx, action, req, &resp)
	if err == nil {
		if verr := ocppx.ValidateResponse(v, action, resp); verr != nil {
			err = fmt.Errorf("%w: %w", ocpp.ErrProtocol, verr)
		}
	}
	d := time.Since(start)
	ms := d.Milliseconds()
	s.mu.Lock()
	s.stats.Calls++
	s.stats.Total += d
	s.stats.Max = max(s.stats.Max, d)
	if err != nil {
		s.stats.Errors++
	}
	s.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		ev := types.StationEvent{Event: "call_error", Version: v.String(), Action: action, DurationMS: &ms, Message: err.Error(), Response: resp}
		var ce *ocpp.CallError
		if errors.As(err, &ce) {
			ev.ErrorCode = string(ce.Code)
		} else if e, ok := errs.As(ocpperr.Wrap(err, action, ocppx.StateChanging(action))); ok {
			ev.ErrorCode = e.Code
		}
		s.emit(ev)
		return nil, err
	}
	s.emit(types.StationEvent{Event: "call_result", Version: v.String(), Action: action, Response: resp, DurationMS: &ms})
	return resp, nil
}

// boot sends BootNotification until it is Accepted. It reports false when the station
// is to stop trying (the context ended, or the connection was lost and will be retried).
func (s *Station) boot(ctx context.Context, reason string) bool {
	m := s.model.Station
	for {
		req := payload.Boot{Vendor: m.Vendor, Model: m.Model, Serial: m.Serial, Firmware: m.Firmware, Reason: reason}.Build(s.version())
		resp, err := s.call(ctx, "BootNotification", req)
		var r struct {
			Status   string `json:"status"`
			Interval int    `json:"interval"`
		}
		wait := 10 * time.Second
		if err == nil {
			_ = json.Unmarshal(resp, &r)
			if r.Interval > 0 {
				wait = time.Duration(r.Interval) * time.Second
			}
			if s.accepted(resp) {
				return true
			}
			s.emit(types.StationEvent{Event: "boot_" + lower(r.Status), Message: fmt.Sprintf("retrying in %s", wait)})
		} else if errors.Is(err, ocpp.ErrNotConnected) || errors.Is(err, ocpp.ErrConnectionLost) || errors.Is(err, ocpp.ErrClosed) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		case t := <-s.tasks: // a TriggerMessage(BootNotification) while Pending
			t(ctx)
			s.mu.Lock()
			booted := s.stats.Booted
			s.mu.Unlock()
			if booted {
				return true
			}
		}
	}
}

// accepted handles a BootNotification response: when it is Accepted, the station is booted
// and heartbeats at the interval the CSMS gave (unless the model sets one).
func (s *Station) accepted(resp json.RawMessage) bool {
	var r struct {
		Status   string `json:"status"`
		Interval int    `json:"interval"`
	}
	if json.Unmarshal(resp, &r) != nil || r.Status != "Accepted" {
		return false
	}
	s.mu.Lock()
	s.stats.Booted = true
	if s.model.Heartbeat == 0 && r.Interval > 0 {
		s.heartbeat = time.Duration(r.Interval) * time.Second
		// The configuration and the device model report the interval in use.
		sec := strconv.Itoa(r.Interval)
		if c, ok := s.conf["HeartbeatInterval"]; ok {
			c.Value = sec
			s.conf["HeartbeatInterval"] = c
		}
		if v, ok := s.vars["OCPPCommCtrlr.HeartbeatInterval"]; ok {
			v.Value = sec
			s.vars["OCPPCommCtrlr.HeartbeatInterval"] = v
		}
	}
	s.mu.Unlock()
	s.emit(types.StationEvent{Event: "booted", Message: fmt.Sprintf("heartbeat every %s", s.heartbeatInterval())})
	return true
}

func lower(s string) string {
	if s == "" {
		return "unknown"
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}

// connectors lists every EVSE/connector pair in model order.
func (s *Station) connectors() [][2]int {
	var out [][2]int
	for _, e := range s.model.EVSEs {
		for _, c := range e.Connectors {
			out = append(out, [2]int{e.ID, c})
		}
	}
	return out
}

func (s *Station) busy(evse, connector int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[strconv.Itoa(evse)+"/"+strconv.Itoa(connector)]
	return ok
}

func (s *Station) status(ctx context.Context, evse, connector int, charging bool) {
	v := s.version()
	st := payload.Status{EVSE: evse, Connector: s.model.connector16(evse, connector), Status: "Available", ErrorCode: "NoError"}
	if v != ocpp.V16 {
		st.Connector = connector
		if charging {
			st.Status = "Occupied"
		}
	} else if charging {
		st.Status = "Charging"
	}
	_, _ = s.call(ctx, "StatusNotification", st.Build(v))
}

func (s *Station) statusAll(ctx context.Context) {
	for _, ec := range s.connectors() {
		s.status(ctx, ec[0], ec[1], s.busy(ec[0], ec[1]))
	}
}

// scheduleSessions schedules the model's sessions once, from the first accepted boot.
func (s *Station) scheduleSessions() {
	s.mu.Lock()
	if s.scheduled {
		s.mu.Unlock()
		return
	}
	s.scheduled = true
	s.mu.Unlock()
	for _, m := range s.model.Sessions {
		sess := &session{evse: m.EVSE, connector: m.Connector, token: m.IDToken, tokenType: m.TokenType, power: m.PowerW,
			interval: m.MeterInterval.D(), duration: m.Duration.D()}
		time.AfterFunc(m.After.D(), func() {
			s.enqueue(func(ctx context.Context) {
				if !s.claim(sess) {
					s.emit(types.StationEvent{Event: "error", Message: fmt.Sprintf("session on EVSE %d connector %d not started: the connector is busy", sess.evse, sess.connector)})
					return
				}
				if s.version() != ocpp.V16 {
					s.setTx(sess, newTransactionID())
				}
				s.startSession(ctx, sess)
			})
		})
	}
}

// claim reserves a connector for a session: the one it names, or the first free one.
func (s *Station) claim(x *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.model.EVSEs {
		if x.evse != 0 && e.ID != x.evse {
			continue
		}
		for _, c := range e.Connectors {
			if x.connector != 0 && c != x.connector {
				continue
			}
			k := strconv.Itoa(e.ID) + "/" + strconv.Itoa(c)
			if _, used := s.sessions[k]; used {
				continue
			}
			x.evse, x.connector = e.ID, c
			s.sessions[k] = x
			return true
		}
	}
	return false
}

func (s *Station) release(x *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range x.timers {
		t.Stop()
	}
	if s.sessions[x.key()] == x {
		delete(s.sessions, x.key())
	}
}

func (s *Station) setTx(x *session, id string) {
	s.mu.Lock()
	x.tx = id
	s.mu.Unlock()
}

func (s *Station) hasTransaction(id string) bool {
	return s.find(id) != nil
}

func (s *Station) find(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.sessions {
		if x.tx != "" && x.tx == id {
			return x
		}
	}
	return nil
}

func (s *Station) startSession(ctx context.Context, x *session) {
	v := s.version()
	if x.interval <= 0 {
		x.interval = s.model.MeterInterval.D()
		if x.interval <= 0 {
			x.interval = time.Minute
		}
	}
	if v != ocpp.V16 {
		s.status(ctx, x.evse, x.connector, true)
	}
	start := payload.TransactionStart{EVSE: x.evse, Connector: s.model.connector16(x.evse, x.connector), Token: x.token,
		TokenType: x.tokenType, Transaction: x.tx, RemoteStartID: x.remoteStartID}
	if start.TokenType == "" {
		start.TokenType = "ISO14443"
	}
	if v != ocpp.V16 {
		start.Connector = x.connector
		if x.remote {
			start.TriggerReason = "RemoteStart"
		}
	}
	action, req := start.Build(v)
	resp, err := s.call(ctx, action, req)
	if err != nil {
		s.release(x)
		return
	}
	var r struct {
		TransactionID int `json:"transactionId"`
		IDTagInfo     struct {
			Status string `json:"status"`
		} `json:"idTagInfo"`
		IDTokenInfo *struct {
			Status string `json:"status"`
		} `json:"idTokenInfo"`
	}
	_ = json.Unmarshal(resp, &r)
	status := r.IDTagInfo.Status
	if v == ocpp.V16 {
		s.setTx(x, strconv.Itoa(r.TransactionID))
		s.status(ctx, x.evse, x.connector, true)
	} else if r.IDTokenInfo != nil {
		status = r.IDTokenInfo.Status
	} else {
		status = "Accepted"
	}
	x.last = time.Now()
	s.mu.Lock()
	s.stats.Sessions++
	s.mu.Unlock()
	s.emit(types.StationEvent{Event: "session_started", Transaction: x.tx,
		Message: fmt.Sprintf("EVSE %d connector %d, token %s, %s", x.evse, x.connector, x.token, status)})
	if status != "Accepted" {
		s.stopSession(ctx, x.tx, "DeAuthorized", "Deauthorized")
		return
	}
	s.scheduleMeter(x)
	if x.duration > 0 {
		id := x.tx
		t := time.AfterFunc(x.duration, func() { s.enqueue(func(ctx context.Context) { s.stopSession(ctx, id, "Local", "StopAuthorized") }) })
		s.mu.Lock()
		x.timers = append(x.timers, t)
		s.mu.Unlock()
	}
}

func (s *Station) scheduleMeter(x *session) {
	t := time.AfterFunc(x.interval, func() {
		s.enqueue(func(ctx context.Context) {
			if s.find(x.tx) != x {
				return
			}
			s.meter(ctx, x)
			s.scheduleMeter(x)
		})
	})
	s.mu.Lock()
	x.timers = append(x.timers, t)
	s.mu.Unlock()
}

func (x *session) advance() {
	now := time.Now()
	x.energy += x.power * now.Sub(x.last).Hours()
	x.last = now
}

func (s *Station) meter(ctx context.Context, x *session) {
	x.advance()
	x.seq++
	u := payload.TransactionUpdate{EVSE: x.evse, Connector: s.model.connector16(x.evse, x.connector), Transaction: x.tx,
		EnergyWh: float64(int64(x.energy)), PowerW: x.power, SeqNo: x.seq}
	action, req, err := u.Build(s.version())
	if err != nil {
		s.emit(types.StationEvent{Event: "error", Message: err.Error()})
		return
	}
	_, _ = s.call(ctx, action, req)
}

// stopSession ends a transaction: reason is the 1.6 reason and the 2.x stoppedReason,
// trigger the 2.x triggerReason.
func (s *Station) stopSession(ctx context.Context, id, reason, trigger string) {
	x := s.find(id)
	if x == nil {
		return
	}
	s.release(x)
	x.advance()
	x.seq++
	v := s.version()
	stop := payload.TransactionStop{EVSE: x.evse, Connector: x.connector, Transaction: x.tx, MeterStopWh: float64(int64(x.energy)),
		Reason: reason, SeqNo: x.seq, TriggerReason: trigger}
	if v == ocpp.V16 && reason == "Local" {
		stop.Reason = "" // Local is 1.6's default reason
	}
	action, req, err := stop.Build(v)
	if err == nil {
		_, _ = s.call(ctx, action, req)
	}
	s.status(ctx, x.evse, x.connector, false)
	s.emit(types.StationEvent{Event: "session_ended", Transaction: x.tx,
		Message: fmt.Sprintf("EVSE %d connector %d, %.0f Wh, %s", x.evse, x.connector, x.energy, reason)})
	s.mu.Lock()
	idleReset := s.resetOnIdle && len(s.sessions) == 0
	s.mu.Unlock()
	if idleReset {
		s.requestReset()
	}
}

func (s *Station) endAll(ctx context.Context) {
	s.mu.Lock()
	var ids []string
	for _, x := range s.sessions {
		if x.tx != "" {
			ids = append(ids, x.tx)
		}
	}
	s.mu.Unlock()
	s.mu.Lock()
	reason := s.resetReason
	s.resetOnIdle = false
	s.mu.Unlock()
	if reason == "" {
		reason = "HardReset"
	}
	trigger := "ResetCommand"
	for _, id := range ids {
		s.stopSession(ctx, id, reason, trigger)
	}
}

func (s *Station) stopTimers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.sessions {
		for _, t := range x.timers {
			t.Stop()
		}
	}
}

// triggered sends a message a TriggerMessage asked for.
func (s *Station) triggered(ctx context.Context, msg string) {
	v := s.version()
	switch msg {
	case "BootNotification":
		m := s.model.Station
		resp, err := s.call(ctx, "BootNotification", payload.Boot{Vendor: m.Vendor, Model: m.Model, Serial: m.Serial, Firmware: m.Firmware, Reason: "Triggered"}.Build(v))
		if err == nil {
			s.accepted(resp)
		}
	case "Heartbeat":
		_, _ = s.call(ctx, "Heartbeat", empty)
	case "StatusNotification":
		s.statusAll(ctx)
	case "MeterValues":
		req, _ := payload.Meter{EVSE: s.model.EVSEs[0].ID, Connector: 1, EnergyWh: 0}.Build(v)
		_, _ = s.call(ctx, "MeterValues", req)
	case "FirmwareStatusNotification", "DiagnosticsStatusNotification", "LogStatusNotification":
		_, _ = s.call(ctx, msg, json.RawMessage(`{"status":"Idle"}`))
	}
}

// notifyReport answers GetBaseReport with the device model, in one NotifyReport.
func (s *Station) notifyReport(ctx context.Context, requestID int) {
	s.mu.Lock()
	data := []map[string]any{}
	for _, k := range sortedKeys(s.vars) {
		v := s.vars[k]
		c, name, _ := cut(k)
		attr := map[string]any{"type": "Actual", "value": v.Value}
		if v.Mutability != "" {
			attr["mutability"] = v.Mutability
		}
		data = append(data, map[string]any{"component": map[string]any{"name": c}, "variable": map[string]any{"name": name},
			"variableAttribute": []map[string]any{attr}, "variableCharacteristics": map[string]any{"dataType": "string", "supportsMonitoring": false}})
	}
	s.mu.Unlock()
	req := marshal(map[string]any{"requestId": requestID, "generatedAt": time.Now().UTC().Format(time.RFC3339), "seqNo": 0, "tbc": false, "reportData": data})
	_, _ = s.call(ctx, "NotifyReport", req)
}

func cut(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == '.' {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}

func newTransactionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
