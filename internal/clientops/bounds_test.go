// SPDX-License-Identifier: MIT

package clientops

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/answer"
	"github.com/otfabric/ocppctl/internal/testcsms"
)

// flooding answers a Heartbeat only after it has sent the station n DataTransfer CALLs of
// about size bytes each.
type flooding struct {
	n, size int
	next    ocpp.Dispatcher
}

func (f flooding) Dispatch(ctx context.Context, p ocpp.Peer, action string, req json.RawMessage) (json.RawMessage, error) {
	if action == "Heartbeat" {
		data := json.RawMessage(`{"vendorId":"x","data":"` + strings.Repeat("d", f.size) + `"}`)
		for range f.n {
			var resp json.RawMessage
			if err := p.Call(ctx, "DataTransfer", data, &resp); err != nil {
				return nil, err
			}
		}
	}
	return f.next.Dispatch(ctx, p, action, req)
}

// `client call` lists what the CSMS sent meanwhile up to the bounds of answer.Log, and
// counts the rest in received_dropped.
func TestCallReceivedWithinBounds(t *testing.T) {
	v := ocpp.V201
	const n, size = 150, 60 << 10
	csms := testcsms.Start(t, server.WithHandler(v, flooding{n: n, size: size, next: testcsms.Answers(v)}))
	cfg := &CallConfig{Common: common(csms.URL+"CSB", "2.0.1"), Action: "Heartbeat"}
	var stderr bytes.Buffer
	res, err := Call(context.Background(), cfg, nil, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, r := range res.Received {
		total += len(r.Request) + len(r.Response)
	}
	if total > answer.MaxKeptBytes || res.ReceivedDropped == 0 || len(res.Received)+res.ReceivedDropped != n {
		t.Fatalf("%d listed with %d payload bytes, %d dropped, of %d", len(res.Received), total, res.ReceivedDropped, n)
	}
	out, err := res.MarshalTextOutput()
	if err != nil || !strings.Contains(out, "more CALLs and SENDs from the peer, answered and not listed") {
		t.Errorf("text output: %v", err)
	}
}
