// SPDX-License-Identifier: MIT

package script

import (
	"encoding/json"
	"testing"
)

// FuzzSubstitute feeds payloads with variables, as scenario files hold them: substitute
// returns valid JSON or an error, and never panics on what a CSMS returned and a step
// saved.
func FuzzSubstitute(f *testing.F) {
	for _, s := range []string{`{"a":"${tx}"}`, `{"a":"x${tok}y${tx}"}`, `{"a":["${obj}",{"b":"${now}${uuid}"}]}`,
		`{"a":"${nope}"}`, `{"a":"${`, `[1,"${identity}"]`, `"${tx}"`, `{}`, `{"a":"$${tx}}"}`} {
		f.Add([]byte(s))
	}
	vars := map[string]any{"tx": float64(7), "tok": "A\"B\x1b", "obj": map[string]any{"k": []any{1.5, nil, true}}}
	f.Fuzz(func(t *testing.T, raw []byte) {
		out, err := substitute(json.RawMessage(raw), vars, "CS\x00")
		if err != nil {
			return
		}
		if varRE.Match(raw) && !json.Valid(out) {
			t.Fatalf("substituted %q into invalid JSON %q", raw, out)
		}
	})
}

// FuzzCheck feeds conditions on a document the CSMS chose: check never panics, and the
// value found at a path equals itself.
func FuzzCheck(f *testing.F) {
	f.Add([]byte(`{"idTagInfo":{"status":"Accepted"},"list":[{"a":1}]}`), "idTagInfo.status")
	f.Add([]byte(`{"list":[{"a":1}]}`), "list[0].a")
	f.Add([]byte(`[[[]]]`), "0.0.0")
	f.Add([]byte(`nope`), "")
	f.Fuzz(func(t *testing.T, doc []byte, path string) {
		present := true
		_ = check(json.RawMessage(doc), map[string]Matcher{path: {Present: &present}, path + ".x": {Equal: "y"}})
		var v any
		if json.Unmarshal(doc, &v) != nil {
			return
		}
		got, ok := lookup(v, path)
		if !ok {
			return
		}
		if fails := check(json.RawMessage(doc), map[string]Matcher{path: {Equal: got}}); len(fails) != 0 {
			t.Fatalf("the value at %q does not equal itself: %v", path, fails)
		}
	})
}
