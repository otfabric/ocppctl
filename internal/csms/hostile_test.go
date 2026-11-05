// SPDX-License-Identifier: MIT

package csms

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"
	"github.com/otfabric/go-ocpp/ocpptest"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/types"
)

// A station picks its identity: with a newline in it (an OCPP 1.6 identity is not
// checked), the text event stream must still be one line per event.
func TestHostileIdentityText(t *testing.T) {
	r := start(t, plainConfig())
	mustDial(t, r.url("CS%0A2026-01-01T00:00:00.000Z%20%20connected%20ADMIN"), ocpp.V16)
	ev := r.ev.waitEvent(t, "connected", "", "")
	if !strings.Contains(ev.Identity, "\n") {
		t.Fatalf("identity %q: the test does not reach the case", ev.Identity)
	}
	text, _ := ev.MarshalTextOutput()
	if strings.Contains(text, "\n") {
		t.Errorf("event text spans lines: %q", text)
	}
}

// A connection the server refuses before the WebSocket upgrade (wrong path, address not
// allowed) must not stay open: otherwise anyone, even from an address --allow refuses, can
// hold any number of idle connections.
func TestRefusedConnectionIsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		allow []string
		path  string
	}{
		"wrong path":  {path: "/a/b"},
		"not allowed": {allow: []string{"10.9.9.9"}, path: "/CS1"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := plainConfig()
			cfg.Allow, cfg.ControlSocket = tc.allow, ""
			r := start(t, cfg)
			c, err := net.Dial("tcp", r.addr)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			if _, err := io.WriteString(c, "GET "+tc.path+" HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if !resp.Close {
				t.Errorf("HTTP %d does not say Connection: close", resp.StatusCode)
			}
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, err := br.ReadByte(); err != io.EOF {
				t.Errorf("the refused connection stays open: %v", err)
			}
		})
	}
}

// timeout_ms is a number from the control socket: a large one must not wrap into a
// negative duration that fails the CALL at once.
func TestControlHugeTimeout(t *testing.T) {
	r := start(t, plainConfig())
	rec := ocpptest.NewRecorder().Answer("Reset", `{"status":"Accepted"}`)
	mustDial(t, r.url("CS1"), ocpp.V201, client.WithHandler(ocpp.V201, rec))
	r.ev.waitEvent(t, "connected", "CS1", "")
	for _, ms := range []int64{math.MaxInt64, math.MaxInt64 / 1000} {
		res := r.call(t, ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Payload: json.RawMessage(`{"type":"Immediate"}`), TimeoutMS: ms})
		if !res.OK {
			t.Errorf("timeout_ms %d: %+v", ms, res.Error)
		}
	}
}

// The control socket's answers can be larger than a request line may be: stations choose
// their identities, and a long list of long ones must still be listed.
func TestControlLargeResponse(t *testing.T) {
	r := start(t, plainConfig())
	long := strings.Repeat("x", 900<<10)
	const n = 5
	for i := range n {
		mustDial(t, r.url(long+string(rune('a'+i))), ocpp.V16)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		res, err := Stations(ctx, &ControlConfig{ControlSocket: r.socket})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Stations) == n {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("%d stations listed", len(res.Stations))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An event of a refused connection names the identity the station sent: it is quoted in
// the text output like any other.
func TestRefusedIdentityText(t *testing.T) {
	cfg := plainConfig()
	cfg.NoAuth, cfg.PasswordsFile = false, writeFile(t, "pw", "CS1:secret\n")
	r := start(t, cfg)
	var he *client.HandshakeError
	if _, err := dial(t, r.url("CS%0Aforged"), ocpp.V16); !errors.As(err, &he) {
		t.Fatalf("want a refusal, got %v", err)
	}
	ev := r.ev.waitEvent(t, "refused", "CS\nforged", "")
	if text, _ := ev.MarshalTextOutput(); strings.Contains(text, "\n") {
		t.Errorf("refused event spans lines: %q", text)
	}
}

// A station that connects again under the same identity replaces its session: the old one
// is reported replaced, the stations list and server call see the new one only.
func TestReconnectSameIdentity(t *testing.T) {
	r := start(t, plainConfig())
	old := ocpptest.NewRecorder().Answer("Reset", `{"status":"Accepted"}`)
	mustDial(t, r.url("CS1"), ocpp.V201, client.WithHandler(ocpp.V201, old))
	first := r.ev.waitEvent(t, "connected", "CS1", "")
	cur := ocpptest.NewRecorder().Answer("Reset", `{"status":"Rejected"}`)
	mustDial(t, r.url("CS1"), ocpp.V16, client.WithHandler(ocpp.V16, cur))
	gone := r.ev.waitEvent(t, "disconnected", "CS1", "")
	if gone.SessionID != first.SessionID || !strings.Contains(gone.Reason, "replaced") {
		t.Errorf("disconnected: %+v", gone)
	}
	second := r.ev.wait(t, func(e types.ServerEvent) bool {
		return e.Event == "connected" && e.SessionID != first.SessionID
	})
	st := r.call(t, ControlRequest{Op: OpStations})
	if !st.OK || len(st.Stations) != 1 || st.Stations[0].SessionID != second.SessionID || st.Stations[0].Version != "1.6" {
		t.Fatalf("stations: %+v", st)
	}
	res := r.call(t, ControlRequest{Op: OpCall, Station: "CS1", Action: "Reset", Payload: json.RawMessage(`{"type":"Soft"}`)})
	if !res.OK || string(res.Result.Response) != `{"status":"Rejected"}` || len(old.Received("Reset")) != 0 {
		t.Errorf("call went to the old session: %+v %+v", res, res.Error)
	}
}

// Several server call at once to one station: each gets its own response.
func TestConcurrentControlCalls(t *testing.T) {
	r := start(t, plainConfig())
	echo := ocpp.DispatcherFunc(func(_ context.Context, _ ocpp.Peer, _ string, p json.RawMessage) (json.RawMessage, error) {
		var req struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(p, &req)
		return json.Marshal(map[string]any{"configurationKey": []map[string]any{{"key": req.Key, "readonly": true}}})
	})
	mustDial(t, r.url("CP1"), ocpp.V16, client.WithHandler(ocpp.V16, echo))
	r.ev.waitEvent(t, "connected", "CP1", "")
	const n = 16
	errc := make(chan error, n)
	for i := range n {
		go func() {
			key := strings.Repeat("k", i+1)
			res, err := request(context.Background(), r.socket, ControlRequest{Op: OpCall, Station: "CP1", Action: "GetConfiguration",
				Raw: true, Payload: json.RawMessage(`{"key":"` + key + `"}`)}, 10*time.Second)
			switch {
			case err != nil:
				errc <- err
			case !strings.Contains(string(res.Result.Response), `"`+key+`"`):
				errc <- errors.New("response of another call: " + string(res.Result.Response))
			default:
				errc <- nil
			}
		}()
	}
	for range n {
		if err := <-errc; err != nil {
			t.Error(err)
		}
	}
}

// The client side of the control socket: what a server that is gone, or not an ocppctl
// server, answers is a control_socket_failed error, never a success.
func TestControlClientBadResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		code   string
	}{
		"closed":         {"", "control_socket_failed"},
		"not json":       {"hello\n", "control_socket_failed"},
		"wrong type":     {`{"ok":"yes"}` + "\n", "control_socket_failed"},
		"no error":       {`{"ok":false}` + "\n", "internal_unexpected"},
		"typed error":    {`{"ok":false,"error":{"kind":"call_error","code":"call_error","message":"m"},"ocpp":{"error_code":"X"}}` + "\n", "call_error"},
		"truncated json": {`{"ok":tr`, "control_socket_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			path := shortDir(t) + "/f.sock"
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()
			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = bufio.NewReader(c).ReadString('\n')
				_, _ = io.WriteString(c, tc.answer)
				_ = c.Close()
			}()
			_, err = request(context.Background(), path, ControlRequest{Op: OpStations}, 5*time.Second)
			if e, ok := errs.As(err); !ok || e.Code != tc.code {
				t.Errorf("got %v, want %s", err, tc.code)
			}
		})
	}
	if _, err := request(context.Background(), "", ControlRequest{Op: OpStations}, time.Second); err == nil {
		t.Error("an empty socket path was accepted")
	}
}

// The control socket answers a line that is not JSON with an error and serves the next
// request on the same connection; a line over maxControlLine ends the connection.
func TestControlMalformedLines(t *testing.T) {
	r := start(t, plainConfig())
	c, err := net.Dial("unix", r.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	for _, line := range []string{"not json", `{"op":1}`, `{"op":"stations"}`} {
		if _, err := io.WriteString(c, line+"\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := br.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var cr ControlResponse
		if err := json.Unmarshal(resp, &cr); err != nil {
			t.Fatal(err)
		}
		if ok := line == `{"op":"stations"}`; cr.OK != ok || (!ok && cr.Error.Code != errs.CodeInputDecodeFailed) {
			t.Errorf("%s: %s", line, resp)
		}
	}
	go func() { _, _ = c.Write(make([]byte, maxControlLine+1)) }()
	if _, err := br.ReadBytes('\n'); err == nil {
		t.Error("an oversized line was answered")
	}
}

// A control socket left behind by a server that is gone is replaced.
func TestControlStaleSocket(t *testing.T) {
	path := shortDir(t) + "/s.sock"
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	// Closing a Unix listener removes its file: keep it, as a crashed server does.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	cfg := plainConfig()
	cfg.ControlSocket = path
	r := start(t, cfg)
	if st := r.call(t, ControlRequest{Op: OpStations}); !st.OK {
		t.Errorf("stations: %+v", st.Error)
	}
}
