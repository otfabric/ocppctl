// SPDX-License-Identifier: MIT

package offline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/errs"
)

// FuzzFrameDecode feeds `frame decode` the frames an operator pastes from a log. Any
// input is a result or an invalid-input error, never a panic, and a result is always
// JSON that --format json can write.
func FuzzFrameDecode(f *testing.F) {
	for _, s := range []string{
		`[2,"19223201","BootNotification",{"chargePointVendor":"VendorX","chargePointModel":"SingleSocketCharger"}]`,
		`[3,"19223201",{"status":"Accepted","currentTime":"2013-02-01T20:53:32.486Z","interval":300}]`,
		`[4,"162376037","NotSupported","SetDisplayMessage not supported",{}]`,
		`[5,"1","FormatViolation","",{}]`,
		`[6,"1","NotifyPeriodicEventStream",{"id":1,"pending":0,"basetime":"2026-01-01T00:00:00Z","data":[]}]`,
		`[2,"1","Nope",{}]`, `[2,"1","Heartbeat",[]]`, `[]`, `[2]`, `{}`, `"x"`, ``, "[2,\"\x1b\",\"Heartbeat\",{}]",
		`[2,"1","Heartbeat",{"a":1e999}]`, `[4,"1","GenericError","d",null]`,
	} {
		for _, v := range []string{"1.6", "2.0.1", "2.1"} {
			f.Add(v, s)
		}
	}
	f.Fuzz(func(t *testing.T, version, frame string) {
		if frame == "-" {
			return // stdin
		}
		res, err := FrameDecode(&FrameDecodeConfig{Version: version, Frame: frame}, strings.NewReader(""))
		if err != nil {
			if res != nil {
				t.Fatalf("a result and an error: %v", err)
			}
			if e, ok := errs.As(err); !ok || e.Kind != errs.KindInvalidInput {
				t.Fatalf("not an invalid-input error: %v", err)
			}
			return
		}
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("the result is not JSON: %v\n%+v", err, res)
		}
		if res.TypeNumber < 2 || res.TypeNumber > 6 {
			t.Fatalf("decoded an impossible frame type: %+v", res)
		}
	})
}
