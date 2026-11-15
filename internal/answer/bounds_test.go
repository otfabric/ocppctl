// SPDX-License-Identifier: MIT

package answer

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
)

// The first MaxKept CALLs and SENDs are kept; the others are answered as before and
// counted.
func TestDefaultKeepsMaxKept(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{0, 1, MaxKept - 1, MaxKept, MaxKept + 1, MaxKept + 7} {
		d := &Default{Version: ocpp.V201}
		for i := range n {
			if i%2 == 0 {
				if resp, err := d.Dispatch(ctx, nil, "ClearCache", json.RawMessage(`{}`)); err != nil || string(resp) != `{"status":"Accepted"}` {
					t.Fatalf("CALL %d: %s, %v", i, resp, err)
				}
			} else if err := d.DispatchSend(ctx, nil, "NotifyPeriodicEventStream", json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		if kept := len(d.Received()); kept != min(n, MaxKept) || kept+d.Dropped() != n {
			t.Errorf("%d CALLs and SENDs: %d kept, %d dropped", n, kept, d.Dropped())
		}
	}
}

// The payloads kept total at most MaxKeptBytes: what a peer sends beyond that is not
// kept, so that the memory held stays within the budget whatever the peer sends.
func TestDefaultKeepsMaxKeptBytes(t *testing.T) {
	ctx := context.Background()
	const size = 32 << 10
	n := 2 * MaxKeptBytes / size
	d := &Default{Version: ocpp.V201}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range n {
		req := json.RawMessage(`{"vendorId":"x","data":"` + strings.Repeat("d", size-30) + `"}`)
		if _, err := d.Dispatch(ctx, nil, "DataTransfer", req); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	got := d.Received()
	total := 0
	for _, r := range got {
		total += len(r.Request) + len(r.Response)
	}
	if total > MaxKeptBytes || total+size+64 <= MaxKeptBytes || len(got)+d.Dropped() != n {
		t.Errorf("%d kept with %d payload bytes, %d dropped, of %d", len(got), total, d.Dropped(), n)
	}
	if held, budget := int64(after.HeapAlloc)-int64(before.HeapAlloc), int64(MaxKeptBytes+(4<<20)); held > budget {
		t.Errorf("the log holds %d bytes of heap, budget %d", held, budget)
	}
	runtime.KeepAlive(d)
}
