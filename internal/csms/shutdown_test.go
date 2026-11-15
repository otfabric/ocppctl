// SPDX-License-Identifier: MIT

package csms

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/types"
)

// Run returns once every session has ended. This station answers the server's close frame
// with the start of a message whose remaining bytes never come, so its session outlasts
// the 5 s of the server's Shutdown: it ends at go-ocpp's bound of the closing handshake
// (10 s). Its disconnected event comes before Run returns, and no event comes after.
func TestRunWaitsForItsSessions(t *testing.T) {
	cfg := plainConfig()
	cfg.Listen, cfg.ControlSocket = "127.0.0.1:0", ""
	ev := newEvents()
	var mu sync.Mutex
	returned := false
	var late []types.ServerEvent
	emit := func(e types.ServerEvent) {
		mu.Lock()
		if returned {
			late = append(late, e)
		}
		mu.Unlock()
		ev.add(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		err := Run(ctx, &cfg, emit, io.Discard, func(a net.Addr) { ready <- a })
		mu.Lock()
		returned = true
		mu.Unlock()
		done <- err
	}()
	var addr net.Addr
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("Run: %v", err)
	}

	c, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := fmt.Fprintf(c, "GET /CSW HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: %s\r\n\r\n",
		addr, ocpp.V16.Subprotocol()); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	ev.waitEvent(t, "connected", "CSW", "")
	stalled := make(chan error, 1)
	go func() {
		// The start of the server's close frame, then a masked text message of 1000
		// bytes of which only the first is sent.
		if _, err := io.ReadFull(br, make([]byte, 2)); err != nil {
			stalled <- err
			return
		}
		_, err := c.Write([]byte{0x81, 0x80 | 0x7e, 0x03, 0xe8, 1, 2, 3, 4, '[' ^ 1})
		stalled <- err
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("Run did not return")
	}
	var names []string
	for _, e := range ev.all() {
		names = append(names, e.Event)
	}
	if last := names[len(names)-1]; last != "disconnected" {
		t.Errorf("Run returned before the session ended: events %v", names)
	}
	if err := <-stalled; err != nil {
		t.Errorf("the station: %v", err)
	}
	_ = c.Close() // the session, if it still runs, ends now
	mu.Lock()
	defer mu.Unlock()
	if len(late) > 0 {
		t.Errorf("events after Run returned: %+v", late)
	}
}
