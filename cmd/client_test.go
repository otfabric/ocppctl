// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpptest"
	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

var allVersions = []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21}

// station runs `ocppctl client <sub> --url <csms><id> --insecure-transport <args>`.
func station(t *testing.T, csms *testcsms.CSMS, id, sub string, args ...string) result {
	t.Helper()
	full := append(strings.Fields("client "+sub), "--url", csms.URL+id, "--insecure-transport")
	return run(t, append(full, args...)...)
}

// envelope is the JSON error object, with its ocpp detail.
type envelope struct {
	Error types.ErrorInfo `json:"error"`
	OCPP  map[string]any  `json:"ocpp"`
}

func (r result) envelope(t *testing.T) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal([]byte(r.stdout), &e); err != nil {
		t.Fatalf("stdout is not a JSON error: %v\n%s", err, r.stdout)
	}
	return e
}

func TestProbe(t *testing.T) {
	csms := testcsms.Start(t)
	var res types.ProbeResult
	station(t, csms, "CS1", "probe", "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if len(res.Accepted) != 1 || res.Accepted[0] != "2.1" || res.Identity != "CS1" || res.Attempts[0].Auth != "none" {
		t.Fatalf("probe: %+v", res)
	}
	station(t, csms, "CS1", "probe", "--each-version", "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if strings.Join(res.Accepted, ",") != "2.1,2.0.1,1.6" {
		t.Fatalf("each version: %+v", res.Accepted)
	}
	station(t, csms, "CS1", "probe", "--each-version").want(t, errs.ExitOK).has(t, "attempt 3", "accepted        2.1, 2.0.1, 1.6")
	station(t, csms, "CS1", "probe", "--compression", "--format", "table").want(t, errs.ExitOK).has(t, "OFFERED", "accepted")
}

func TestProbeRefused(t *testing.T) {
	csms := testcsms.Start(t, server.WithAuthenticator(server.BasicAuth(func(_ context.Context, id string) (string, bool) {
		return "s3cret", id == "CS1"
	})))
	r := station(t, csms, "CS1", "probe", "--format", "json").want(t, errs.ExitTransport)
	if e := r.envelope(t); e.Error.Code != "handshake_refused" || e.OCPP["http_status"] != float64(401) {
		t.Fatalf("refused: %+v", e)
	}
	t.Setenv("OCPPCTL_PASSWORD", "s3cret")
	station(t, csms, "CS1", "probe").want(t, errs.ExitOK).has(t, "basic")
	r = station(t, csms, "CS2", "probe", "--format", "json").want(t, errs.ExitTransport)
	if e := r.envelope(t); e.OCPP["http_status"] != float64(404) {
		t.Fatalf("unknown identity: %+v", e)
	}
	t.Setenv("OCPPCTL_PASSWORD", "")
	pw := filepath.Join(t.TempDir(), "pw")
	_ = os.WriteFile(pw, []byte("s3cret\n"), 0o600)
	station(t, csms, "CS1", "call", "Heartbeat", "--password-file", pw).want(t, errs.ExitOK)
	station(t, csms, "CS1", "call", "Heartbeat", "--password-file", pw+"x").want(t, errs.ExitInvalidInput)
	// --each-version: refused everywhere is the first refusal
	station(t, csms, "CS1", "probe", "--each-version").want(t, errs.ExitTransport).errHas(t, "401")
}

func TestTLS(t *testing.T) {
	rec := testcsms.Answers(ocpp.V201)
	srv, err := server.New(server.WithHandler(ocpp.V201, rec), server.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewTLSServer(srv)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()); hs.Close() })
	ca := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: hs.Certificate().Raw}), 0o600)
	url := "wss" + strings.TrimPrefix(hs.URL, "https") + "/CS1"

	var res types.ProbeResult
	run(t, "client", "probe", "--url", url, "--tls-ca", ca, "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if tl := res.Attempts[0].TLS; tl == nil || !strings.HasPrefix(tl.Version, "TLS 1.") || tl.ClientCert {
		t.Fatalf("tls: %+v", res.Attempts[0])
	}
	run(t, "client", "probe", "--url", url, "--tls-ca", ca).want(t, errs.ExitOK).has(t, "tls             TLS 1.")
	r := run(t, "client", "probe", "--url", url, "--format", "json").want(t, errs.ExitTransport)
	if e := r.envelope(t); e.Error.Code != "transport_tls_failed" {
		t.Fatalf("untrusted: %+v", e)
	}
	run(t, "client", "call", "Heartbeat", "--url", url, "--tls-ca", ca, "--ocpp-version", "2.0.1").want(t, errs.ExitOK)
	run(t, "client", "probe", "--url", url, "--tls-ca", ca+"x").want(t, errs.ExitInvalidInput)
	run(t, "client", "probe", "--url", url, "--tls-cert", ca).want(t, errs.ExitInvalidInput)
	run(t, "client", "probe", "--url", strings.Replace(url, "wss", "ws", 1), "--tls-ca", ca, "--insecure-transport").want(t, errs.ExitInvalidInput)
}

func TestCall(t *testing.T) {
	csms := testcsms.Start(t)
	for _, v := range allVersions {
		t.Run(v.String(), func(t *testing.T) {
			var res types.CallResult
			station(t, csms, "CS1", "call", "Heartbeat", "--ocpp-version", v.String(), "--format", "json").want(t, errs.ExitOK).decode(t, &res)
			if res.Version != v.String() || res.Action != "Heartbeat" || !res.Validated || !strings.Contains(string(res.Response), testcsms.Time) {
				t.Fatalf("call: %+v", res)
			}
			station(t, csms, "CS1", "call", "Heartbeat", "--ocpp-version", v.String(), "--boot").want(t, errs.ExitOK).
				has(t, "Heartbeat answered").errHas(t, "BootNotification accepted")
			station(t, csms, "CS1", "call", "Heartbeat", "--ocpp-version", v.String(), "--format", "table").want(t, errs.ExitOK).has(t, "currentTime")
			// --debug traces every frame on stderr
			station(t, csms, "CS1", "call", "Heartbeat", "--ocpp-version", v.String(), "--debug").want(t, errs.ExitOK).
				errHas(t, `CS1 > [2,`, `"Heartbeat",{}]`, `CS1 < [3,`)
		})
	}
	station(t, csms, "CS1", "call", "Heartbeat", "--payload", `{"x":1}`, "--ocpp-version", "1.6").want(t, errs.ExitInvalidInput).errHas(t, "x")
	station(t, csms, "CS1", "call", "Bogus").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "call", "Reset", "--ocpp-version", "2.0.1").want(t, errs.ExitInvalidInput).errHas(t, "--raw")
	station(t, csms, "CS1", "call", "NotifyPeriodicEventStream", "--ocpp-version", "2.1").want(t, errs.ExitInvalidInput).errHas(t, "client send")
	// --raw sends what it is given; the test CSMS does not validate
	station(t, csms, "CS1", "call", "Heartbeat", "--raw", "--payload", `{"x":1}`).want(t, errs.ExitOK).has(t, "Heartbeat answered")
	runIn(t, t.Context(), "{}", "client", "call", "Heartbeat", "--url", csms.URL+"CS1", "--insecure-transport", "--payload", "-").want(t, errs.ExitOK)
	// with several versions, the request is checked after connecting
	station(t, csms, "CS1", "call", "Heartbeat", "--payload", `{"x":1}`).want(t, errs.ExitInvalidInput)
	run(t, "client", "call", "Heartbeat", "--url", "http://x/CS1").want(t, errs.ExitInvalidInput)
	run(t, "client", "call", "Heartbeat", "--url", "ws://u:p@x/CS1", "--insecure-transport").want(t, errs.ExitInvalidInput).errHas(t, "credentials")
	run(t, "client", "call", "Heartbeat", "--url", "ws://x/", "--insecure-transport").want(t, errs.ExitInvalidInput).errHas(t, "identity")
	station(t, csms, "CS1", "call", "Heartbeat", "--timeout", "x").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "call", "Heartbeat", "--header", "novalue").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "call", "Heartbeat", "--header", "X-Test=1", "--ocpp-version", "9").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "call", "Heartbeat", "--ocpp-version", "1.6,1.6").want(t, errs.ExitInvalidInput)
	run(t, "client", "call", "Heartbeat", "--url", "ws://127.0.0.1:1/CS1", "--insecure-transport", "--connect-timeout", "1s").want(t, errs.ExitTransport)
}

func TestCallErrors(t *testing.T) {
	csms := testcsms.Start(t)
	csms.Recorder(ocpp.V201).AnswerError("DataTransfer", &ocpp.CallError{Code: ocpp.NotImplemented, Description: "nope", Details: json.RawMessage(`{"why":"test"}`)})
	r := station(t, csms, "CS1", "data-transfer", "--vendor", "x", "--ocpp-version", "2.0.1", "--format", "json").want(t, errs.ExitProtocol)
	if e := r.envelope(t); e.Error.Code != "call_error" || e.OCPP["error_code"] != "NotImplemented" || e.OCPP["description"] != "nope" {
		t.Fatalf("CALLERROR: %+v", e)
	}
	csms.Recorder(ocpp.V16).Answer("Heartbeat", `{}`)
	r = station(t, csms, "CS1", "heartbeat", "--ocpp-version", "1.6", "--format", "json").want(t, errs.ExitProtocol)
	if e := r.envelope(t); e.Error.Code != "invalid_response" {
		t.Fatalf("invalid response: %+v", e)
	}
	csms.Recorder(ocpp.V21).Answer("BootNotification", `{"status":"Rejected","currentTime":"2026-01-01T00:00:00Z","interval":60}`)
	r = station(t, csms, "CS1", "heartbeat", "--boot", "--format", "json").want(t, errs.ExitProtocol)
	if e := r.envelope(t); e.Error.Code != "boot_not_accepted" || e.OCPP["status"] != "Rejected" {
		t.Fatalf("boot: %+v", e)
	}
	// a rejected boot from the boot shortcut is a result, not a failure
	station(t, csms, "CS1", "boot").want(t, errs.ExitOK).has(t, "Rejected")
	station(t, csms, "CS1", "boot", "--boot").want(t, errs.ExitUsage)
	if e := station(t, csms, "CS1", "boot", "--boot", "--format", "json").want(t, errs.ExitUsage).envelope(t); e.Error.Code != "invalid_flag_combination" {
		t.Fatalf("--boot with boot: %+v", e)
	}
}

func TestTimeout(t *testing.T) {
	slow := ocpp.DispatcherFunc(func(ctx context.Context, _ ocpp.Peer, _ string, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
		return json.RawMessage(`{}`), nil
	})
	_, url := ocpptest.CSMS(t, server.WithHandler(ocpp.V16, slow))
	r := run(t, "client", "call", "StatusNotification", "--url", url+"CS1", "--insecure-transport", "--ocpp-version", "1.6", "--timeout", "200ms",
		"--payload", `{"connectorId":1,"errorCode":"NoError","status":"Available"}`, "--format", "json").want(t, errs.ExitTimeout)
	if e := r.envelope(t); e.Error.Code != "outcome_unknown" {
		t.Fatalf("state-changing timeout: %+v", e)
	}
	r = run(t, "client", "heartbeat", "--url", url+"CS1", "--insecure-transport", "--ocpp-version", "1.6", "--timeout", "200ms", "--format", "json").want(t, errs.ExitTimeout)
	if e := r.envelope(t); e.Error.Code != "request_timeout" {
		t.Fatalf("read-only timeout: %+v", e)
	}
	r = run(t, "client", "probe", "--url", url+"CS1", "--insecure-transport", "--ocpp-version", "2.0.1", "--format", "json").want(t, errs.ExitTransport)
	if e := r.envelope(t); e.Error.Code != "no_common_version" {
		t.Fatalf("no common version: %+v", e)
	}
}

func TestSend(t *testing.T) {
	csms := testcsms.Start(t)
	p := `{"id":1,"pending":0,"basetime":"2026-01-01T00:00:00Z","data":[{"t":0,"v":"230"}]}`
	var res types.SendResult
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", p, "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if !res.Sent || res.Version != "2.1" {
		t.Fatalf("send: %+v", res)
	}
	csms.Recorder(ocpp.V21).Wait(t, "NotifyPeriodicEventStream")
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", p).want(t, errs.ExitOK).has(t, "never answered")
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", p, "--format", "table").want(t, errs.ExitOK).has(t, "SENT")
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", p, "--ocpp-version", "1.6").want(t, errs.ExitInvalidInput).errHas(t, "2.1")
	station(t, csms, "CS1", "send", "Heartbeat").want(t, errs.ExitInvalidInput).errHas(t, "client call")
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", "{}").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "send", "Bogus").want(t, errs.ExitInvalidInput)
	station(t, csms, "CS1", "send", "NotifyPeriodicEventStream", "--payload", "[").want(t, errs.ExitInvalidInput)
}

func TestShortcuts(t *testing.T) {
	csms := testcsms.Start(t)
	for _, v := range allVersions {
		t.Run(v.String(), func(t *testing.T) {
			ver := []string{"--ocpp-version", v.String()}
			rec := csms.Recorder(v)
			sc := func(sub string, args ...string) result {
				return station(t, csms, "CS"+strings.ReplaceAll(v.String(), ".", ""), sub, append(args, ver...)...)
			}
			sc("boot", "--vendor", "V", "--model", "M", "--serial", "S", "--firmware", "F").want(t, errs.ExitOK).has(t, "Accepted")
			sc("heartbeat").want(t, errs.ExitOK)
			sc("authorize", "--id-token", "AABB").want(t, errs.ExitOK).has(t, "Accepted")
			sc("status", "--status", "Available").want(t, errs.ExitOK)
			sc("meter", "--energy-wh", "1500.5", "--power-w", "7400", "--transaction", "7").want(t, errs.ExitOK)
			sc("data-transfer", "--vendor", "com.example", "--message-id", "m", "--data", `{"a":1}`).want(t, errs.ExitOK)
			var start types.CallResult
			sc("transaction start", "--id-token", "AABB", "--format", "json").want(t, errs.ExitOK).decode(t, &start)
			tx := "7"
			if v != ocpp.V16 {
				var req struct {
					TransactionInfo struct {
						TransactionID string `json:"transactionId"`
					} `json:"transactionInfo"`
				}
				_ = json.Unmarshal(start.Request, &req)
				tx = req.TransactionInfo.TransactionID
				if len(tx) != 36 {
					t.Fatalf("generated transaction id %q", tx)
				}
			}
			sc("transaction stop", "--transaction", tx, "--energy-wh", "3000", "--reason", "Local").want(t, errs.ExitOK)
			for _, a := range []string{"BootNotification", "Heartbeat", "Authorize", "StatusNotification", "MeterValues", "DataTransfer"} {
				rec.Wait(t, a)
			}
			if v == ocpp.V16 {
				rec.Wait(t, "StartTransaction")
				rec.Wait(t, "StopTransaction")
				sc("meter", "--energy-wh", "1", "--transaction", "abc").want(t, errs.ExitInvalidInput)
				sc("transaction stop", "--transaction", "abc").want(t, errs.ExitInvalidInput)
			} else if n := len(rec.Received("TransactionEvent")); n != 2 {
				t.Fatalf("%d TransactionEvents", n)
			}
		})
	}
	run(t, "client", "boot", "--ocpp-version", "2.0.1", "--payload-only").want(t, errs.ExitOK).has(t, `"vendorName": "OT Fabric"`)
	run(t, "client", "boot", "--payload-only").want(t, errs.ExitInvalidInput).errHas(t, "exactly one")
	run(t, "client", "transaction", "stop", "--transaction", "x", "--ocpp-version", "1.6", "--payload-only").want(t, errs.ExitInvalidInput)
	run(t, "client", "status", "--status", "Bogus", "--ocpp-version", "2.0.1", "--payload-only").want(t, errs.ExitInvalidInput)
	run(t, "client", "heartbeat", "--ocpp-version", "9", "--payload-only").want(t, errs.ExitInvalidInput)
	run(t, "client", "meter", "--energy-wh", "5", "--ocpp-version", "1.6", "--payload-only", "--format", "table").want(t, errs.ExitOK).has(t, "meterValue")
	run(t, "client", "transaction").want(t, errs.ExitOK).has(t, "start", "stop")
	run(t, "client").want(t, errs.ExitOK).has(t, "Security profiles")
	run(t, "client", "authorize").want(t, errs.ExitUsage)
}
