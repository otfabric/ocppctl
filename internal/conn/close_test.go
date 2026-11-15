// SPDX-License-Identifier: MIT

package conn

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // RFC 6455 §4.2.2 derives Sec-WebSocket-Accept with SHA-1
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/testcsms"
)

// upgrade completes the server side of a WebSocket handshake on c by hand, selecting
// subprotocol.
func upgrade(c net.Conn, subprotocol string) error {
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		return err
	}
	h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec // see the import
	_, err = fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", base64.StdEncoding.EncodeToString(h[:]), subprotocol)
	return err
}

// The HTTP client of the handshake has a plain *http.Transport: go-ocpp bounds the
// closing handshake of a connection only with one.
func TestHTTPClientHasPlainTransport(t *testing.T) {
	p, err := Target{URL: "wss://h/CS1", Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s"}.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if tr, ok := httpClient(p).Transport.(*http.Transport); !ok || tr.TLSClientConfig != p.TLS || tr.Proxy == nil {
		t.Fatalf("transport %T, want an *http.Transport with the TLS configuration and the proxy of the environment", httpClient(p).Transport)
	}
}

// What the handshake negotiated is recorded: the status, the subprotocol and the
// compression.
func TestHandshakeRecorded(t *testing.T) {
	csms := testcsms.Start(t)
	for _, compression := range []bool{false, true} {
		p, err := Target{URL: csms.URL + "CSH", Versions: []string{"2.0.1"}, Timeout: "5s", ConnectTimeout: "5s",
			InsecureTransport: true, Compression: compression}.Parse()
		if err != nil {
			t.Fatal(err)
		}
		c, err := Dial(context.Background(), p, Options{})
		if err != nil {
			t.Fatal(err)
		}
		h := c.Handshake
		_ = c.Stop()
		// The monotonic clock may report a duration of zero (Windows).
		if h.Status != http.StatusSwitchingProtocols || h.Subprotocol != ocpp.V201.Subprotocol() || h.Compressed() != compression ||
			h.TLS != nil || h.Duration < 0 {
			t.Errorf("compression %t: %+v", compression, h)
		}
	}
}

// A CSMS that completes the upgrade but not the closing handshake does not hold Stop:
// go-ocpp closes the connection once its bound of the closing handshake (10 s) has
// passed. This CSMS answers the client's close frame with the start of a message whose
// remaining bytes never come, and keeps the connection open.
func TestStopWhenTheCloseIsNotCompleted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	release := make(chan struct{})
	defer close(release)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if upgrade(c, ocpp.V16.Subprotocol()) != nil {
			return
		}
		// The start of the client's close frame, then a text message of 1000 bytes of
		// which only the first is sent.
		if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
			return
		}
		_, _ = c.Write([]byte{0x81, 0x7e, 0x03, 0xe8, '['})
		<-release
	}()
	p, err := Target{URL: fmt.Sprintf("ws://%s/CS1", ln.Addr()), Versions: []string{"1.6"}, Timeout: "5s",
		ConnectTimeout: "5s", InsecureTransport: true}.Parse()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(context.Background(), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		_ = c.Stop()
		close(stopped)
	}()
	// ShutdownTimeout, go-ocpp's bound of the closing handshake, and a margin.
	limit := ShutdownTimeout + 10*time.Second + 15*time.Second
	select {
	case <-stopped:
	case <-time.After(limit):
		t.Fatalf("Stop had not returned after %s", limit)
	}
}
