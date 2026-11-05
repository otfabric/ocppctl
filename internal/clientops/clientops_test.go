// SPDX-License-Identifier: MIT

package clientops

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/payload"
	"github.com/otfabric/ocppctl/internal/testcsms"
	"github.com/otfabric/ocppctl/internal/types"
)

func TestAuth(t *testing.T) {
	cert := &tls.Config{Certificates: []tls.Certificate{{}}}
	for want, p := range map[string]*conn.Parsed{
		"none":              {},
		"basic":             {Password: "x"},
		"certificate":       {TLS: cert},
		"basic+certificate": {Password: "x", TLS: cert},
	} {
		if got := auth(p); got != want {
			t.Errorf("auth = %s, want %s", got, want)
		}
	}
	if tlsInfo(nil, nil) != nil {
		t.Error("tlsInfo of a ws:// connection")
	}
	if i := tlsInfo(&tls.ConnectionState{Version: tls.VersionTLS13}, cert); i == nil || !i.ClientCert || i.Version != "TLS 1.3" {
		t.Errorf("tlsInfo: %+v", i)
	}
}

func TestCheckAction(t *testing.T) {
	if a, err := checkAction(ocpp.V16, "Bogus", true); err != nil || a.Name != "Bogus" {
		t.Errorf("raw unknown action: %+v, %v", a, err)
	}
	if _, err := checkAction(ocpp.V16, "Reset", false); err == nil || !strings.Contains(err.Error(), "--raw") {
		t.Errorf("an action of the CSMS: %v", err)
	}
	if _, err := checkAction(ocpp.V16, "Reset", true); err != nil {
		t.Errorf("an action of the CSMS with --raw: %v", err)
	}
}

func common(url string, versions ...string) Common {
	c := NewCommon()
	c.URL, c.InsecureTransport, c.Versions = url, true, versions
	return c
}

// CALLs the CSMS sends while a call waits for its response are answered and listed.
func TestCallReceived(t *testing.T) {
	csms := testcsms.Start(t)
	v := ocpp.V16
	started := make(chan struct{})
	csms.Recorder(v).Answer("Heartbeat", `{"currentTime":"`+testcsms.Time+`"}`)
	go func() {
		<-started
		sess := ocpptest.Session(t, csms.Server, "CSR")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var resp json.RawMessage
		_ = sess.Call(ctx, "ClearCache", json.RawMessage(`{}`), &resp)
		_ = sess.Call(ctx, "GetLocalListVersion", json.RawMessage(`{}`), &resp)
	}()
	cfg := &CallConfig{Common: common(csms.URL+"CSR", "1.6"), Action: "DataTransfer", Payload: `{"vendorId":"x"}`}
	var stderr bytes.Buffer
	s, err := open(context.Background(), &cfg.Common, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	close(started)
	deadline := time.Now().Add(10 * time.Second)
	for len(s.received()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	res, err := callOn(context.Background(), s, cfg.Action, json.RawMessage(cfg.Payload), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Received) != 2 || res.Received[0].Action != "ClearCache" || res.Received[1].ErrorCode == "" && res.Received[1].Response == nil {
		t.Fatalf("received: %+v", res.Received)
	}
}

// A BootNotification of --boot that is not Accepted is a negative answer, with its status.
func TestBootNotAccepted(t *testing.T) {
	csms := testcsms.Start(t)
	csms.Recorder(ocpp.V201).Answer("BootNotification", `{"status":"Rejected","currentTime":"`+testcsms.Time+`","interval":60}`)
	c := common(csms.URL+"CSB", "2.0.1")
	c.Boot = true
	_, err := Shortcut(context.Background(), &c, false, HeartbeatBuilder(), &bytes.Buffer{})
	if e, ok := errs.As(err); !ok || e.Code != errs.CodeBootNotAccepted || e.OCPP.(map[string]string)["status"] != "Rejected" {
		t.Fatalf("%v", err)
	}
}

// A builder that fails is invalid input, connected or not.
func TestBuilderFails(t *testing.T) {
	csms := testcsms.Start(t)
	fail := func(ocpp.Version) (string, json.RawMessage, error) {
		return "", nil, errs.InvalidInput(errs.CodeInvalidInput, "no", nil)
	}
	c := common(csms.URL+"CSF", "1.6")
	if _, err := Shortcut(context.Background(), &c, false, fail, &bytes.Buffer{}); err == nil {
		t.Fatal("connected: a failing builder passed")
	}
	if _, err := Shortcut(context.Background(), &c, true, fail, nil); err == nil {
		t.Fatal("payload only: a failing builder passed")
	}
	c.Versions = []string{"1.6", "2.1"}
	if _, err := Shortcut(context.Background(), &c, true, HeartbeatBuilder(), nil); err == nil {
		t.Fatal("payload only with two versions")
	}
	c.Versions = []string{"9"}
	if _, err := Shortcut(context.Background(), &c, true, HeartbeatBuilder(), nil); err == nil {
		t.Fatal("payload only with an unknown version")
	}
	r, err := Shortcut(context.Background(), &Common{Target: conn.Target{Versions: []string{"2.0.1"}}}, true, StartBuilder(payloadStart()), nil)
	if err != nil || len(r.(*types.PayloadResult).Payload) == 0 {
		t.Fatalf("payload only: %v, %v", r, err)
	}
}

// A SEND that the CSMS chose a version without SEND for, or a CALL given to send.
func TestSendRefused(t *testing.T) {
	csms := testcsms.Start(t)
	cfg := &SendConfig{Common: common(csms.URL+"CSS", "2.1"), Action: "BootNotification", Payload: "{}"}
	if _, err := Send(context.Background(), cfg, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "client call") {
		t.Fatalf("a CALL as a SEND: %v", err)
	}
	cfg.Action, cfg.Raw = "Heartbeat", true
	if res, err := Send(context.Background(), cfg, nil, &bytes.Buffer{}); err != nil || !res.Sent {
		t.Fatalf("a raw SEND: %+v, %v", res, err)
	}
	cfg.URL = " "
	if _, err := Send(context.Background(), cfg, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("no URL")
	}
}

func TestNewTransactionID(t *testing.T) {
	a, b := NewTransactionID(), NewTransactionID()
	if a == b || len(a) != 36 || strings.Count(a, "-") != 4 {
		t.Fatalf("%s, %s", a, b)
	}
}

func payloadStart() payload.TransactionStart {
	return payload.TransactionStart{EVSE: 1, Connector: 1, Token: "AB", TokenType: "ISO14443"}
}
