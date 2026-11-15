// SPDX-License-Identifier: MIT

package csms

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// FuzzParse feeds CSMS model files. A model that validates must still
// validate after being written out and read back.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"",
		"boot: 1",
		"boot: {status: Nope}",
		`{"boot":{"status":"Accepted"}}`,
		"boot:\n  status: Pending\n",
		"actions:\n- action: DataTransfer\n  answer: reject\n",
		"actions:\n- action: DataTransfer\n  versions: [\"1.6\"]\n  response: {status: Accepted}\n",
	} {
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

// FuzzCheckCSMSCall feeds the checks of a control socket CALL: whatever the action and
// payload, a refusal is a typed error and nothing panics.
func FuzzCheckCSMSCall(f *testing.F) {
	for _, s := range []struct {
		action, payload string
		raw             bool
	}{
		{"Reset", `{"type":"Immediate"}`, false},
		{"Reset", `{"type":"Soft"}`, false},
		{"Reset", `[1]`, true},
		{"BootNotification", `{}`, false},
		{"Bogus", `{}`, true},
		{"", `{}`, true},
		{"GetVariables", `{"getVariableData":[{"component":{"name":"x"},"variable":{"name":"y"}}]}`, false},
		{"NotifyPeriodicEventStream", `{}`, true},
		{"Reset", `{"type":"Immediate","x":` + strings.Repeat("[", 100) + strings.Repeat("]", 100) + `}`, false},
	} {
		f.Add(s.action, []byte(s.payload), s.raw)
	}
	f.Fuzz(func(t *testing.T, action string, payload []byte, raw bool) {
		for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
			if err := checkCSMSCall(v, action, payload, raw); err != nil {
				if _, ok := errs.As(err); !ok {
					t.Fatalf("%s %s: untyped error %v", v, action, err)
				}
			}
		}
	})
}

var answerVersions = []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21}

// answerSeeds reads testdata/answer-seeds.json: by version and action, requests that pass
// the request schema. For every CALL a station sends there is one with only the required
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

// Every CALL a station sends in each version has a seed, and every seed passes its
// request schema: a fuzz target that starts from them starts past the validation.
func TestAnswerSeeds(t *testing.T) {
	seeds := answerSeeds(t)
	for _, v := range answerVersions {
		want := map[string]bool{}
		for _, a := range ocppx.Actions(v) {
			if a.Kind == "call" && a.From != ocppx.FromCSMS {
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
				t.Errorf("%s %s: a seed of an action that is not a CALL of a station", v, name)
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

// FuzzCSMSAnswer feeds the CALLs of a station to the CSMS model, starting from the seeds
// of every CALL of every version. The model has a token list, so that both an accepted
// and a refused token are answered. Whatever arrives, the answer is a CALLRESULT that
// passes the action's response schema or a CALLERROR, and a request that does not pass
// its request schema gets a CALLERROR.
func FuzzCSMSAnswer(f *testing.F) {
	seeds := answerSeeds(f)
	for i, v := range answerVersions {
		bv := seeds[v.String()]
		for _, action := range slices.Sorted(maps.Keys(bv)) {
			for _, p := range bv[action] {
				f.Add(uint8(i), action, []byte(p))
			}
		}
	}
	m := Default()
	m.Authorization.Tokens = []string{"x"}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, vi uint8, action string, req []byte) {
		v := answerVersions[int(vi)%len(answerVersions)]
		a := newAnswerer(m, func(types.ServerEvent) {})
		a.now = func() time.Time { return now }
		resp, err := a.answer(v, "CSF", action, req)
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
