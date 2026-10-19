// SPDX-License-Identifier: MIT

package csms

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/types"
)

const testModel = `
boot:
  status: Accepted
  interval: 60
stations:
  - identity: PENDING1
    boot_status: Pending
authorization:
  tokens: [GOOD]
transactions:
  first_id: 100
actions:
  - action: DataTransfer
    stations: [REJ]
    answer: reject
  - action: SecurityEventNotification
    answer: not-supported
  - action: FirmwareStatusNotification
    versions: ["1.6"]
    response: {}
`

func status(m map[string]any, key string) string {
	if key == "" {
		s, _ := m["status"].(string)
		return s
	}
	sub, _ := m[key].(map[string]any)
	s, _ := sub["status"].(string)
	return s
}

func callError(t *testing.T, err error) *ocpp.CallError {
	t.Helper()
	var ce *ocpp.CallError
	if !errors.As(err, &ce) {
		t.Fatalf("want a CALLERROR, got %v", err)
	}
	return ce
}

func TestAnswers16(t *testing.T) {
	cfg := plainConfig()
	cfg.Config = writeFile(t, "m.yaml", testModel)
	r := start(t, cfg)
	c := mustDial(t, r.url("CP16"), ocpp.V16)
	conn := r.ev.waitEvent(t, "connected", "CP16", "")
	if conn.Version != "1.6" || *conn.TLS || conn.Remote == "" {
		t.Errorf("connected: %+v", conn)
	}

	boot := mustCall(t, c, "BootNotification", `{"chargePointVendor":"V","chargePointModel":"M"}`)
	if status(boot, "") != "Accepted" || boot["interval"] != float64(60) || boot["currentTime"] == "" {
		t.Errorf("boot: %v", boot)
	}
	if hb := mustCall(t, c, "Heartbeat", `{}`); hb["currentTime"] == nil {
		t.Errorf("heartbeat: %v", hb)
	}
	if a := mustCall(t, c, "Authorize", `{"idTag":"GOOD"}`); status(a, "idTagInfo") != "Accepted" {
		t.Errorf("authorize good: %v", a)
	}
	if a := mustCall(t, c, "Authorize", `{"idTag":"BAD"}`); status(a, "idTagInfo") != "Invalid" {
		t.Errorf("authorize bad: %v", a)
	}
	for i, want := range []float64{100, 101} {
		st := mustCall(t, c, "StartTransaction", `{"connectorId":1,"idTag":"GOOD","meterStart":0,"timestamp":"2026-01-01T00:00:00Z"}`)
		if st["transactionId"] != want || status(st, "idTagInfo") != "Accepted" {
			t.Errorf("start %d: %v", i, st)
		}
	}
	if st := mustCall(t, c, "StopTransaction", `{"transactionId":100,"meterStop":10,"timestamp":"2026-01-01T00:00:00Z","idTag":"BAD"}`); status(st, "idTagInfo") != "Invalid" {
		t.Errorf("stop: %v", st)
	}
	if st := mustCall(t, c, "StopTransaction", `{"transactionId":101,"meterStop":10,"timestamp":"2026-01-01T00:00:00Z"}`); len(st) != 0 {
		t.Errorf("stop without idTag: %v", st)
	}
	mustCall(t, c, "StatusNotification", `{"connectorId":1,"errorCode":"NoError","status":"Available"}`)
	mustCall(t, c, "MeterValues", `{"connectorId":1,"meterValue":[{"timestamp":"2026-01-01T00:00:00Z","sampledValue":[{"value":"1"}]}]}`)
	mustCall(t, c, "FirmwareStatusNotification", `{"status":"Idle"}`)
	if dt := mustCall(t, c, "DataTransfer", `{"vendorId":"X"}`); status(dt, "") != "UnknownVendorId" {
		t.Errorf("data transfer: %v", dt)
	}
	// An invalid request is answered with the CALLERROR of the violation.
	_, err := call(t, c, "Authorize", `{}`)
	_ = callError(t, err)
	ans := r.ev.wait(t, func(e types.ServerEvent) bool {
		return e.Event == "answered" && e.Action == "Authorize" && e.ErrorCode != ""
	})
	if ans.Identity != "CP16" {
		t.Errorf("answered: %+v", ans)
	}
	got := r.ev.waitEvent(t, "call_received", "CP16", "StartTransaction")
	if !strings.Contains(string(got.Request), `"GOOD"`) {
		t.Errorf("call_received: %+v", got)
	}

	_ = c.Close()
	disc := r.ev.waitEvent(t, "disconnected", "CP16", "")
	if disc.Reason == "" {
		t.Errorf("disconnected: %+v", disc)
	}
}

func TestAnswers2x(t *testing.T) {
	for _, v := range []ocpp.Version{ocpp.V201, ocpp.V21} {
		t.Run(v.String(), func(t *testing.T) {
			cfg := plainConfig()
			cfg.Config = writeFile(t, "m.yaml", testModel)
			r := start(t, cfg)
			c := mustDial(t, r.url("CS1"), v, client.WithCompression())
			if conn := r.ev.waitEvent(t, "connected", "CS1", ""); conn.Version != v.String() || !*conn.Compression {
				t.Errorf("connected: %+v", conn)
			}
			boot := mustCall(t, c, "BootNotification", `{"reason":"PowerUp","chargingStation":{"model":"M","vendorName":"V"}}`)
			if status(boot, "") != "Accepted" || boot["interval"] != float64(60) {
				t.Errorf("boot: %v", boot)
			}
			tok := `{"idToken":{"idToken":"%s","type":"ISO14443"}}`
			if a := mustCall(t, c, "Authorize", fmt.Sprintf(tok, "GOOD")); status(a, "idTokenInfo") != "Accepted" {
				t.Errorf("authorize good: %v", a)
			}
			if a := mustCall(t, c, "Authorize", fmt.Sprintf(tok, "BAD")); status(a, "idTokenInfo") != "Invalid" {
				t.Errorf("authorize bad: %v", a)
			}
			te := `{"eventType":"Started","timestamp":"2026-01-01T00:00:00Z","triggerReason":"Authorized","seqNo":0,"transactionInfo":{"transactionId":"T1"}%s}`
			if r := mustCall(t, c, "TransactionEvent", fmt.Sprintf(te, `,"idToken":{"idToken":"GOOD","type":"ISO14443"}`)); status(r, "idTokenInfo") != "Accepted" {
				t.Errorf("transaction event: %v", r)
			}
			if r := mustCall(t, c, "TransactionEvent", fmt.Sprintf(te, "")); len(r) != 0 {
				t.Errorf("transaction event without token: %v", r)
			}
			mustCall(t, c, "StatusNotification", `{"timestamp":"2026-01-01T00:00:00Z","connectorStatus":"Available","evseId":1,"connectorId":1}`)
			mustCall(t, c, "NotifyReport", `{"requestId":1,"generatedAt":"2026-01-01T00:00:00Z","seqNo":0}`)
			_, err := call(t, c, "SecurityEventNotification", `{"type":"x","timestamp":"2026-01-01T00:00:00Z"}`)
			if ce := callError(t, err); ce.Code != ocpp.NotSupported {
				t.Errorf("not-supported override: %v", ce)
			}
			// An override for 1.6 only does not apply.
			mustCall(t, c, "FirmwareStatusNotification", `{"status":"Idle"}`)
			// No answer is schema-valid: NotSupported.
			_, err = call(t, c, "Get15118EVCertificate", `{"iso15118SchemaVersion":"x","action":"Install","exiRequest":"x"}`)
			if ce := callError(t, err); ce.Code != ocpp.NotSupported {
				t.Errorf("no answer: %v", ce)
			}

			// Per-station answers: a Pending boot, a rejected DataTransfer.
			p := mustDial(t, r.url("PENDING1"), v)
			if boot := mustCall(t, p, "BootNotification", `{"reason":"PowerUp","chargingStation":{"model":"M","vendorName":"V"}}`); status(boot, "") != "Pending" {
				t.Errorf("pending boot: %v", boot)
			}
			rej := mustDial(t, r.url("REJ"), v)
			if dt := mustCall(t, rej, "DataTransfer", `{"vendorId":"X"}`); status(dt, "") != "Rejected" {
				t.Errorf("rejected data transfer: %v", dt)
			}
			if dt := mustCall(t, c, "DataTransfer", `{"vendorId":"X"}`); status(dt, "") != "UnknownVendorId" {
				t.Errorf("data transfer: %v", dt)
			}
		})
	}
}

func TestSend21(t *testing.T) {
	r := start(t, plainConfig())
	c := mustDial(t, r.url("CS21"), ocpp.V21)
	ctx := context.Background()
	if err := c.Send(ctx, "NotifyPeriodicEventStream", json.RawMessage(`{"id":1,"pending":0,"basetime":"2026-01-01T00:00:00Z","data":[{"t":0,"v":"230"}]}`)); err != nil {
		t.Fatal(err)
	}
	if ev := r.ev.waitEvent(t, "send_received", "CS21", "NotifyPeriodicEventStream"); ev.ErrorCode != "" {
		t.Errorf("valid SEND: %+v", ev)
	}
	if err := c.Send(ctx, "NotifyPeriodicEventStream", json.RawMessage(`{"id":"x"}`)); err != nil {
		t.Fatal(err)
	}
	r.ev.wait(t, func(e types.ServerEvent) bool { return e.Event == "send_received" && e.ErrorCode != "" })
}

func TestBasicAuth(t *testing.T) {
	cfg := NewStartConfig()
	cfg.InsecureTransport = true
	cfg.Config = writeFile(t, "m.yaml", "stations:\n  - identity: CS1\n    password: model-secret-1\n")
	cfg.PasswordsFile = writeFile(t, "pw.txt", "# comment\n\nCS2:file-secret-2\n")
	r := start(t, cfg)

	for id, pw := range map[string]string{"CS1": "model-secret-1", "CS2": "file-secret-2"} {
		mustDial(t, r.url(id), ocpp.V201, client.WithBasicAuth(id, pw))
		r.ev.waitEvent(t, "connected", id, "")
	}
	var he *client.HandshakeError
	_, err := dial(t, r.url("CS1"), ocpp.V201, client.WithBasicAuth("CS1", "wrong"))
	if !errors.As(err, &he) || he.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %v", err)
	}
	_, err = dial(t, r.url("NOPE"), ocpp.V201, client.WithBasicAuth("NOPE", "x"))
	if !errors.As(err, &he) || he.StatusCode != http.StatusNotFound {
		t.Errorf("unknown station: %v", err)
	}
	_, err = dial(t, r.url("CS1"), ocpp.V201)
	if !errors.As(err, &he) || he.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credentials: %v", err)
	}
	r.ev.wait(t, func(e types.ServerEvent) bool { return e.Event == "refused" && strings.Contains(e.Reason, "404") })
	r.ev.wait(t, func(e types.ServerEvent) bool {
		return e.Event == "refused" && strings.Contains(e.Reason, "no credentials")
	})
	r.ev.wait(t, func(e types.ServerEvent) bool {
		return e.Event == "refused" && e.Reason == "authentication failed (HTTP 401)"
	})
	for _, e := range r.ev.all() {
		b, _ := json.Marshal(e)
		if strings.Contains(string(b), "secret") || strings.Contains(string(b), "wrong") {
			t.Errorf("an event shows a credential: %s", b)
		}
	}
}

func TestTLSProfiles(t *testing.T) {
	p := newPKI(t, "CS3")
	cfg := NewStartConfig()
	cfg.TLSCert, cfg.TLSKey, cfg.TLSClientCA = p.serverCert, p.serverKey, p.caFile
	cfg.Config = writeFile(t, "m.yaml", "stations:\n  - identity: CS2\n    password: pw-two\n")
	r := start(t, cfg)
	url := func(id string) string { return "wss://" + r.addr + "/" + id }

	// Profile 3: the certificate's CN is the identity.
	c3, err := client.New(url("CS3"), client.WithVersions(ocpp.V201),
		client.WithTLS(&tls.Config{RootCAs: p.pool, Certificates: []tls.Certificate{p.client}, MinVersion: tls.VersionTLS12}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c3.Connect(ctx); err != nil {
		t.Fatalf("profile 3: %v", err)
	}
	defer func() { _ = c3.Close() }()
	if ev := r.ev.waitEvent(t, "connected", "CS3", ""); !*ev.TLS {
		t.Errorf("connected: %+v", ev)
	}
	// Profile 2 next to it: a password over TLS, no certificate.
	c2, err := client.New(url("CS2"), client.WithVersions(ocpp.V16), client.WithBasicAuth("CS2", "pw-two"),
		client.WithTLS(&tls.Config{RootCAs: p.pool, MinVersion: tls.VersionTLS12}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Connect(ctx); err != nil {
		t.Fatalf("profile 2: %v", err)
	}
	defer func() { _ = c2.Close() }()
	r.ev.waitEvent(t, "connected", "CS2", "")

	res := r.call(t, ControlRequest{Op: OpStations})
	if !res.OK || len(res.Stations) != 2 || !res.Stations[0].TLS || res.Stations[0].Identity != "CS2" {
		t.Errorf("stations: %+v", res)
	}
}

func (r *running) call(t *testing.T, req ControlRequest) *ControlResponse {
	t.Helper()
	resp, err := request(context.Background(), r.socket, req, 10*time.Second)
	if err != nil {
		e, _ := errs.As(err)
		return &ControlResponse{Error: &types.ErrorInfo{Kind: string(e.Kind), Code: e.Code, Message: e.Message}}
	}
	return resp
}

func TestControl(t *testing.T) {
	r := start(t, plainConfig())
	rec := ocpptest.NewRecorder().
		Answer("Reset", `{"status":"Accepted"}`).
		Answer("GetVariables", `{"getVariableResult":[]}`).
		Answer("ClearCache", `{"bogus":true}`).
		AnswerError("UnlockConnector", ocpp.NewCallError(ocpp.InternalError, "stuck"))
	slow := ocpp.DispatcherFunc(func(ctx context.Context, _ ocpp.Peer, _ string, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	mustDial(t, r.url("CS1"), ocpp.V201, client.WithHandler(ocpp.V201, rec))
	mustDial(t, r.url("CP16"), ocpp.V16, client.WithHandler(ocpp.V16, slow))
	r.ev.waitEvent(t, "connected", "CS1", "")
	r.ev.waitEvent(t, "connected", "CP16", "")

	st := r.call(t, ControlRequest{Op: OpStations})
	if !st.OK || len(st.Stations) != 2 || st.Stations[0].Identity != "CP16" || st.Stations[1].Version != "2.0.1" {
		t.Fatalf("stations: %+v", st)
	}

	res := r.call(t, ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Payload: json.RawMessage(`{"type":"Immediate"}`)})
	if !res.OK || res.Result.Action != "Reset" || string(res.Result.Response) != `{"status":"Accepted"}` || res.Result.Target != "CS1" || !res.Result.Validated {
		t.Errorf("reset: %+v %+v", res, res.Error)
	}
	if got := rec.Wait(t, "Reset"); string(got) != `{"type":"Immediate"}` {
		t.Errorf("station got %s", got)
	}
	r.ev.waitEvent(t, "call_sent", "CS1", "Reset")
	if ev := r.ev.waitEvent(t, "call_result", "CS1", "Reset"); ev.DurationMS == nil || len(ev.Response) == 0 {
		t.Errorf("call_result: %+v", ev)
	}

	for name, tc := range map[string]struct {
		req  ControlRequest
		code string
	}{
		"not connected":   {ControlRequest{Op: OpCall, Station: "NONE", Action: "Reset", Payload: json.RawMessage(`{"type":"Immediate"}`)}, errs.CodeStationNotConnected},
		"invalid payload": {ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Payload: json.RawMessage(`{"type":"Soft"}`)}, errs.CodeInvalidPayload},
		"wrong direction": {ControlRequest{Op: OpCall, Station: "CS1", Action: "BootNotification"}, errs.CodeUnknownAction},
		"unknown action":  {ControlRequest{Op: OpCall, Station: "CS1", Action: "Bogus"}, errs.CodeUnknownAction},
		"not an object":   {ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Raw: true, Payload: json.RawMessage(`[1]`)}, errs.CodeInvalidPayload},
		"call error":      {ControlRequest{Op: OpCall, Station: "CS1", Action: "UnlockConnector", Payload: json.RawMessage(`{"evseId":1,"connectorId":1}`)}, errs.CodeCallError},
		"bad response":    {ControlRequest{Op: OpCall, Station: "CS1", Action: "ClearCache"}, errs.CodeInvalidResponse},
		"timeout":         {ControlRequest{Op: OpCall, Station: "CP16", Action: "Reset", Payload: json.RawMessage(`{"type":"Soft"}`), TimeoutMS: 200}, errs.CodeOutcomeUnknown},
		"read timeout":    {ControlRequest{Op: OpCall, Station: "CP16", Action: "GetConfiguration", TimeoutMS: 200}, errs.CodeRequestTimeout},
		"unknown op":      {ControlRequest{Op: "x"}, errs.CodeInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.call(t, tc.req)
			if res.OK || res.Error == nil || res.Error.Code != tc.code {
				t.Errorf("got %+v %+v, want %s", res, res.Error, tc.code)
			}
		})
	}
	// A raw CALL is sent unvalidated.
	res = r.call(t, ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Raw: true, Payload: json.RawMessage(`{"type":"Immediate","extra":1}`)})
	if !res.OK || res.Result.Validated {
		t.Errorf("raw: %+v %+v", res, res.Error)
	}
	// The CALLERROR detail travels with the error.
	resp, err := request(context.Background(), r.socket, ControlRequest{Op: OpCall, Station: "CS1", Action: "UnlockConnector",
		Payload: json.RawMessage(`{"evseId":1,"connectorId":1}`)}, 5*time.Second)
	e, _ := errs.As(err)
	if resp != nil || e == nil || errs.ExitCodeForKind(e.Kind) != 6 || !strings.Contains(fmt.Sprint(string(e.OCPP.(json.RawMessage))), "InternalError") {
		t.Errorf("call error: %v %+v", resp, e)
	}
}

func TestControlSocketLifecycle(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s.sock")
	cfg := plainConfig()
	cfg.ControlSocket = sock
	r := start(t, cfg)
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v %v", fi, err)
	}
	// A second server on the same socket is refused.
	cfg2 := plainConfig()
	cfg2.Listen = "127.0.0.1:0"
	cfg2.ControlSocket = sock
	err = Run(context.Background(), &cfg2, func(types.ServerEvent) {}, nil, nil)
	if e, ok := errs.As(err); !ok || e.Code != errs.CodeControlSocketFailed {
		t.Errorf("second server: %v", err)
	}
	// A malformed request gets an error response, and the connection goes on.
	r.stop()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("the socket was not removed: %v", err)
	}
	if _, err := request(context.Background(), sock, ControlRequest{Op: OpStations}, time.Second); err == nil {
		t.Error("a stopped server answered")
	} else if e, _ := errs.As(err); e.Code != errs.CodeControlSocketFailed {
		t.Errorf("no server: %v", err)
	}

	// A file that is not a socket is left alone.
	plain := filepath.Join(dir, "f.sock")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg3 := plainConfig()
	cfg3.Listen, cfg3.ControlSocket = "127.0.0.1:0", plain
	if err := Run(context.Background(), &cfg3, func(types.ServerEvent) {}, nil, nil); err == nil {
		t.Error("a regular file was taken as the control socket")
	}
}

func TestPathAllowAndLimits(t *testing.T) {
	cfg := plainConfig()
	cfg.Path = "ocpp"
	cfg.Allow = []string{"127.0.0.0/8"}
	cfg.MaxStations = 1
	cfg.RateLimit = 2.5
	cfg.PingInterval = "1m"
	cfg.ControlSocket = ""
	r := start(t, cfg)
	var he *client.HandshakeError
	if _, err := dial(t, r.url("CS1"), ocpp.V201); !errors.As(err, &he) || he.StatusCode != http.StatusNotFound {
		t.Errorf("outside the path: %v", err)
	}
	mustDial(t, "ws://"+r.addr+"/ocpp/CS1", ocpp.V201)
	if _, err := dial(t, "ws://"+r.addr+"/ocpp/CS2", ocpp.V201); !errors.As(err, &he) || he.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("over the limit: %v", err)
	}

	cfg = plainConfig()
	cfg.Allow = []string{"10.1.2.3"}
	r = start(t, cfg)
	if _, err := dial(t, r.url("CS1"), ocpp.V201); !errors.As(err, &he) || he.StatusCode != http.StatusForbidden {
		t.Errorf("not allowed: %v", err)
	}
	r.ev.wait(t, func(e types.ServerEvent) bool { return e.Event == "refused" && strings.Contains(e.Reason, "403") })
}

func TestDuration(t *testing.T) {
	cfg := plainConfig()
	cfg.Listen, cfg.ControlSocket, cfg.Duration = "127.0.0.1:0", "", "100ms"
	ev := newEvents()
	if err := Run(context.Background(), &cfg, ev.add, nil, nil); err != nil {
		t.Fatal(err)
	}
	all := ev.all()
	if len(all) != 2 || all[0].Event != "listening" || all[1].Event != "stopping" || all[0].ControlSocket != "" {
		t.Errorf("events: %+v", all)
	}
	// A busy port.
	r := start(t, plainConfig())
	cfg.Listen = r.addr
	if e, ok := errs.As(Run(context.Background(), &cfg, ev.add, nil, nil)); !ok || e.Code != errs.CodeListenFailed {
		t.Errorf("busy port: %v", e)
	}
}

func TestCheck(t *testing.T) {
	p := newPKI(t, "x")
	pw := writeFile(t, "pw", "CS1:x\n")
	for name, tc := range map[string]struct {
		mod  func(*StartConfig)
		code string
	}{
		"ok":                {func(c *StartConfig) {}, ""},
		"plain":             {func(c *StartConfig) { c.InsecureTransport = false }, errs.CodeInvalidFlagCombination},
		"no auth":           {func(c *StartConfig) { c.NoAuth = false }, errs.CodeInvalidFlagCombination},
		"no-auth and pw":    {func(c *StartConfig) { c.PasswordsFile = pw }, errs.CodeInvalidFlagCombination},
		"version":           {func(c *StartConfig) { c.Versions = []string{"3.0"} }, errs.CodeInvalidInput},
		"listen":            {func(c *StartConfig) { c.Listen = "nope" }, errs.CodeInvalidInput},
		"cert alone":        {func(c *StartConfig) { c.TLSCert = p.serverCert }, errs.CodeInvalidFlagCombination},
		"client ca alone":   {func(c *StartConfig) { c.TLSClientCA = p.caFile }, errs.CodeInvalidFlagCombination},
		"bad cert":          {func(c *StartConfig) { c.TLSCert, c.TLSKey = p.caFile, p.serverKey }, errs.CodeInputDecodeFailed},
		"missing ca":        {func(c *StartConfig) { c.TLSCert, c.TLSKey, c.TLSClientCA = p.serverCert, p.serverKey, "/nope" }, errs.CodeInputFileOpenFailed},
		"empty ca":          {func(c *StartConfig) { c.TLSCert, c.TLSKey, c.TLSClientCA = p.serverCert, p.serverKey, p.serverKey }, errs.CodeInputDecodeFailed},
		"allow":             {func(c *StartConfig) { c.Allow = []string{"x"} }, errs.CodeInvalidInput},
		"ping":              {func(c *StartConfig) { c.PingInterval = "soon" }, errs.CodeInvalidInput},
		"duration":          {func(c *StartConfig) { c.Duration = "-1s" }, errs.CodeInvalidInput},
		"negative":          {func(c *StartConfig) { c.MaxStations = -1 }, errs.CodeInvalidInput},
		"missing model":     {func(c *StartConfig) { c.Config = "/nope.yaml" }, errs.CodeInputFileOpenFailed},
		"bad model":         {func(c *StartConfig) { c.Config = writeFile(t, "m.yaml", "boot: {status: Maybe}\n") }, errs.CodeInvalidConfig},
		"missing passwords": {func(c *StartConfig) { c.NoAuth, c.PasswordsFile = false, "/nope" }, errs.CodeInputFileOpenFailed},
		"bad passwords":     {func(c *StartConfig) { c.NoAuth, c.PasswordsFile = false, writeFile(t, "pw", "secret-without-colon\n") }, errs.CodeInputDecodeFailed},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := plainConfig()
			tc.mod(&cfg)
			err := Check(&cfg)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			e, ok := errs.As(err)
			if !ok || e.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			if strings.Contains(e.Message, "secret-without-colon") {
				t.Error("the passwords file line was echoed")
			}
			if err := Run(context.Background(), &cfg, func(types.ServerEvent) {}, nil, nil); err == nil {
				t.Error("Run accepted what Check refused")
			}
		})
	}
}
