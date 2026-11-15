// SPDX-License-Identifier: MIT

package script

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/answer"
	"github.com/otfabric/ocppctl/internal/testcsms"
)

// The CALLs no step waits for are listed once each, the first answer.MaxKept of them; the
// others are answered as well and counted in unexpected_dropped.
func TestUnexpectedWithinBounds(t *testing.T) {
	const extra = 5
	flooded := make(chan error, 1)
	csms := testcsms.Start(t, server.WithSessionHandler(func(s *server.Session) {
		go func() {
			ctx := context.Background()
			for range answer.MaxKept + extra {
				var resp json.RawMessage
				if err := s.Call(ctx, "ClearCache", json.RawMessage(`{}`), &resp); err != nil {
					flooded <- err
					return
				}
			}
			var resp json.RawMessage
			flooded <- s.Call(ctx, "Reset", json.RawMessage(`{"type":"Immediate"}`), &resp)
		}()
	}, nil))
	res, _, err := runScenario(t, csms.URL+"CSU", `version: "2.0.1"
steps:
  - wait_for: Reset
    timeout: 1m
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-flooded; err != nil {
		t.Fatalf("the CSMS's CALLs: %v", err)
	}
	if res.Passed != 1 || len(res.Unexpected) != answer.MaxKept || res.UnexpectedDropped != extra {
		t.Fatalf("passed %d, %d unexpected listed, %d dropped", res.Passed, len(res.Unexpected), res.UnexpectedDropped)
	}
	if u := res.Unexpected[0]; u.Action != "ClearCache" || string(u.Response) != `{"status":"Accepted"}` {
		t.Errorf("first unexpected: %+v", u)
	}
	out, err := res.MarshalTextOutput()
	if err != nil || !strings.Contains(out, "received 5 more CALLs and SENDs that no step waited for") {
		t.Errorf("text output: %v\n%s", err, out)
	}
}

// An expect_no_call step names the first CALLs and counts the others.
func TestExpectNoCallNamesTheFirst(t *testing.T) {
	r := &runner{quiet: true}
	for range maxNoise + 3 {
		r.noisy("ClearCache")
	}
	if len(r.noise) != maxNoise || r.more != 3 {
		t.Fatalf("%d named, %d more", len(r.noise), r.more)
	}
}
