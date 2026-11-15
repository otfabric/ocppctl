// SPDX-License-Identifier: MIT

package station

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// FuzzParse feeds model files. A model that validates must still validate
// after being written out and read back: `client generate-config` and the
// documented configuration rely on the two directions agreeing.
func FuzzParse(f *testing.F) {
	f.Add([]byte(DemoYAML))
	for _, s := range []string{"", "station: 1", "evses: [{id: 0}]", `{"station":{"vendor":"v"}}`, "heartbeat_interval: nonsense\n"} {
		f.Add([]byte(s))
	}
	versions := []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil {
			if m != nil {
				t.Fatalf("a model and an error: %v", err)
			}
			return
		}
		if m.Validate(versions) != nil {
			return
		}
		out, err := yaml.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("written model does not parse: %v\n%s", err, out)
		}
		if err := again.Validate(versions); err != nil {
			t.Fatalf("written model does not validate: %v\n%s", err, out)
		}
	})
}

// FuzzExpandURL feeds identity ranges: ExpandURL returns at once, never more than
// MaxStations URLs, and every URL is the input with the range replaced by a number.
func FuzzExpandURL(f *testing.F) {
	for _, s := range []string{"ws://h/CS{001..100}", "ws://h/CS{1..1}", "ws://h/CS{0..99999999999999999999}",
		"ws://h/CS{9223372036854775807..9223372036854775807}", "ws://h/{1..2}/{3..4}", "ws://h/CS{5..1}", "ws://h/CS{..}", "ws://h/CS"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, u string) {
		urls, err := ExpandURL(u)
		if err != nil {
			return
		}
		if len(urls) == 0 || len(urls) > MaxStations {
			t.Fatalf("%d URLs", len(urls))
		}
		loc := rangeRE.FindStringSubmatchIndex(u)
		if loc == nil {
			if len(urls) != 1 || urls[0] != u {
				t.Fatalf("no range, but %v", urls)
			}
			return
		}
		for _, x := range urls {
			if !strings.HasPrefix(x, u[:loc[0]]) || !strings.HasSuffix(x, u[loc[1]:]) {
				t.Fatalf("%q is not %q with a number", x, u)
			}
		}
	})
}

var answerVersions = []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21}

// answerSeeds reads testdata/answer-seeds.json: by version and action, requests that pass
// the request schema. For every CALL the CSMS sends there is one with only the required
// fields and one with every field, besides the requests of the table tests.
func answerSeeds(tb testing.TB) map[string]map[string][]json.RawMessage {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "answer-seeds.json"))
	if err != nil {
		tb.Fatal(err)
	}
	var seeds map[string]map[string][]json.RawMessage
	if err := json.Unmarshal(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), &seeds); err != nil {
		tb.Fatal(err)
	}
	return seeds
}

// Every CALL the CSMS sends in each version has a seed, and every seed passes its
// request schema: a fuzz target that starts from them starts past the validation.
func TestAnswerSeeds(t *testing.T) {
	seeds := answerSeeds(t)
	for _, v := range answerVersions {
		want := map[string]bool{}
		for _, a := range ocppx.Actions(v) {
			if a.Kind == "call" && a.From != ocppx.FromStation {
				want[a.Name] = true
			}
		}
		for name := range want {
			if len(seeds[v.String()][name]) == 0 {
				t.Errorf("%s %s: no seed", v, name)
			}
		}
		for name, payloads := range seeds[v.String()] {
			if !want[name] {
				t.Errorf("%s %s: a seed of an action that is not a CALL of the CSMS", v, name)
			}
			for _, p := range payloads {
				if err := ocppx.ValidateRequest(v, name, p); err != nil {
					t.Errorf("%s %s %s: %v", v, name, p, err)
				}
			}
		}
	}
	if len(seeds) != len(answerVersions) {
		t.Errorf("seeds of %d versions, want %d", len(seeds), len(answerVersions))
	}
}

// FuzzStationAnswer feeds the CALLs of a CSMS to the built-in station, starting from the
// seeds of every CALL of every version. Whatever arrives, the answer is a CALLRESULT that
// passes the action's response schema or a CALLERROR, and a request that does not pass
// its request schema gets a CALLERROR.
func FuzzStationAnswer(f *testing.F) {
	seeds := answerSeeds(f)
	for i, v := range answerVersions {
		bv := seeds[v.String()]
		for _, action := range slices.Sorted(maps.Keys(bv)) {
			for _, p := range bv[action] {
				f.Add(uint8(i), action, []byte(p))
			}
		}
	}
	tg := conn.NewTarget()
	tg.URL, tg.Versions, tg.InsecureTransport = "ws://h/CSF", []string{"1.6", "2.0.1", "2.1"}, true
	p, err := tg.Parse()
	if err != nil {
		f.Fatal(err)
	}
	m := Demo()
	f.Fuzz(func(t *testing.T, vi uint8, action string, req []byte) {
		v := answerVersions[int(vi)%len(answerVersions)]
		d := &dispatcher{s: New(Options{Parsed: p, Model: m, Emit: func(types.StationEvent) {}}), v: v}
		resp, err := d.answer(context.Background(), action, req)
		if err != nil {
			if _, ok := errors.AsType[*ocpp.CallError](err); !ok {
				t.Fatalf("%s %s: %v is not a CALLERROR", v, action, err)
			}
			if resp != nil {
				t.Fatalf("%s %s: a CALLERROR and the response %s", v, action, resp)
			}
			return
		}
		if verr := ocppx.ValidateRequest(v, action, req); verr != nil {
			t.Fatalf("%s %s: a request that does not pass its schema (%v) was answered %s", v, action, verr, resp)
		}
		if verr := ocppx.ValidateResponse(v, action, resp); verr != nil {
			t.Fatalf("%s %s: the answer %s to %s does not pass the response schema: %v", v, action, resp, req, verr)
		}
	})
}
