// SPDX-License-Identifier: MIT

package script

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"a":{"b":[{"c":1},{"c":"two"}]},"n":null,"s":"x"}`), &doc)
	for path, want := range map[string]string{
		"a.b[1].c": `"two"`,
		"a.b.0.c":  `1`,
		"a.b[0]":   `{"c":1}`,
		"n":        `null`,
		"":         canonical(doc),
		"..s..":    `"x"`,
	} {
		got, ok := lookup(doc, path)
		if !ok || canonical(got) != want {
			t.Errorf("lookup(%q) = %v, %v; want %s", path, got, ok, want)
		}
	}
	for _, path := range []string{"a.x", "a.b[2]", "a.b[-1]", "a.b.c", "s.x", "n.x", "a.b[1].c.d", "a.b[99999999999999999999]"} {
		if got, ok := lookup(doc, path); ok {
			t.Errorf("lookup(%q) = %v, want absent", path, got)
		}
	}
}

func TestCanonical(t *testing.T) {
	if canonical(300) != canonical(300.0) || canonical(map[string]any{"b": 1, "a": 2}) != `{"a":2,"b":1}` {
		t.Fatal("canonical forms differ")
	}
	// A value JSON cannot hold falls back to its Go form.
	if got := canonical(complex(1, 2)); got != "(1+2i)" {
		t.Fatalf("canonical of a complex number: %q", got)
	}
}

func TestCheckRegexOnNonString(t *testing.T) {
	present := true
	fails := check(json.RawMessage(`{"n":300,"o":{"a":1}}`), map[string]Matcher{
		"n": {Regex: regexp.MustCompile(`^3\d\d$`)},
		"o": {Regex: regexp.MustCompile(`"a":1`)},
		"p": {Present: &present},
	})
	if len(fails) != 1 || fails[0] != "p is absent" {
		t.Fatalf("%v", fails)
	}
	// A document that is not JSON has no fields.
	if fails := check(json.RawMessage(`nope`), map[string]Matcher{"x": {Equal: 1}}); len(fails) != 1 {
		t.Fatalf("not JSON: %v", fails)
	}
}

func TestSubstitute(t *testing.T) {
	vars := map[string]any{"tx": float64(7), "tok": "AB", "obj": map[string]any{"k": "v"}}
	got, err := substitute(json.RawMessage(`{"a":"${tx}","b":"tx=${tx} tok=${tok}","c":["${obj}","${identity}"],"d":"${obj}!","e":"$${tok}"}`), vars, "CS1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":7,"b":"tx=7 tok=AB","c":[{"k":"v"},"CS1"],"d":"{\"k\":\"v\"}!","e":"$AB"}` {
		t.Fatalf("substitute: %s", got)
	}
	got, err = substitute(json.RawMessage(`{"t":"${now}","u":"${uuid}"}`), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	_ = json.Unmarshal(got, &m)
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`).MatchString(m["t"]) ||
		!regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(m["u"]) {
		t.Fatalf("built-ins: %v", m)
	}
	for _, in := range []string{`{"a":"${nope}"}`, `{"a":"x${nope}"}`} {
		if _, err := substitute(json.RawMessage(in), vars, ""); err == nil || !strings.Contains(err.Error(), "${nope}") {
			t.Errorf("%s: %v", in, err)
		}
	}
	if _, err := substitute(json.RawMessage(`{"a":"${x}"`), vars, ""); err == nil {
		t.Error("broken JSON with a variable passed")
	}
	// Without a variable the payload is passed on as it is.
	if got, err := substitute(json.RawMessage(`{ "a" : 1 }`), vars, ""); err != nil || string(got) != `{ "a" : 1 }` {
		t.Errorf("untouched: %s, %v", got, err)
	}
}
