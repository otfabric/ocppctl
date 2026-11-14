// SPDX-License-Identifier: MIT

package csms

import (
	"context"
	"net"
	"testing"
)

// oneConn is a listener that hands out one connection, then fails.
type oneConn struct {
	net.Listener
	conn net.Conn
}

func (l *oneConn) Accept() (net.Conn, error) {
	if c := l.conn; c != nil {
		l.conn = nil
		return c, nil
	}
	return nil, net.ErrClosed
}

// A connection accepted after close has begun is closed at once, and serve
// returns without adding it to the connections close waits for.
func TestControlServeAfterClose(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	ct := &control{ln: &oneConn{conn: a}, open: map[net.Conn]bool{}, closed: true}
	ct.serve(context.Background())
	if len(ct.open) != 0 {
		t.Fatalf("open = %d, want 0", len(ct.open))
	}
	if _, err := b.Write([]byte("x")); err == nil {
		t.Error("the connection accepted after close is still open")
	}
	ct.wg.Wait()
}
