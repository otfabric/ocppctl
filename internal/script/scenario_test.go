// SPDX-License-Identifier: MIT

package script

import (
	"encoding/json"
	"strings"
	"testing"
)

// A condition whose operator has no value, or one of the wrong shape, is refused when the
// scenario is read. It used to change its meaning silently: "regex:" with nothing after
// it matched everything, "present:" meant absent and "one_of:" compared with null, so a
// CI run passed a step that checked nothing.
func TestMatcherOperatorValues(t *testing.T) {
	for _, cond := range []string{
		`{regex: }`,
		`{regex: ~}`,
		`{regex: [a]}`,
		`{regex: {a: b}}`,
		`{present: }`,
		`{present: ~}`,
		`{present: [true]}`,
		`{one_of: }`,
		`{one_of: ~}`,
		`{one_of: a}`,
		`{regex: "^A", present: true}`,
	} {
		_, err := Parse([]byte("steps:\n  - call: Heartbeat\n    expect:\n      status: " + cond + "\n"))
		if err == nil {
			t.Errorf("%s: accepted", cond)
		}
	}
	// What the documentation shows still parses, and means what it says.
	s, err := Parse([]byte(`steps:
  - call: BootNotification
    expect:
      interval: {present: true}
      currentTime: {regex: "^20[0-9]{2}-"}
      status: {one_of: [Accepted, Pending]}
      statusInfo: {present: false}
      plain: 300
      empty: {}
      nested: {a: b}
`))
	if err != nil {
		t.Fatal(err)
	}
	fails := check(json.RawMessage(`{"interval":300,"currentTime":"2026-01-01T00:00:00Z","status":"Pending","plain":300.0,"empty":{},"nested":{"a":"b"}}`), s.Steps[0].Expect)
	if len(fails) != 0 {
		t.Fatalf("documented conditions: %v", fails)
	}
	fails = check(json.RawMessage(`{"currentTime":"1999","status":"Rejected","statusInfo":{},"plain":"300","nested":{"a":"c"}}`), s.Steps[0].Expect)
	if want := 7; len(fails) != want {
		t.Fatalf("%d failures, want %d: %s", len(fails), want, strings.Join(fails, "; "))
	}
}

// "@" without a file name is not an empty payload.
func TestPayloadAtWithoutFile(t *testing.T) {
	for _, p := range []string{`"@"`, `"@ "`} {
		if _, err := Parse([]byte("steps:\n  - call: Heartbeat\n    payload: " + p + "\n")); err == nil {
			t.Errorf("payload %s: accepted", p)
		}
	}
}
