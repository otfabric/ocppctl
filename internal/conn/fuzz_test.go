// SPDX-License-Identifier: MIT

package conn

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzReadPayload feeds --payload arguments, inline and from stdin: whatever
// is accepted must be a JSON object, byte for byte what was given.
func FuzzReadPayload(f *testing.F) {
	for _, s := range []string{`{}`, `{"a":1}`, `[]`, `null`, `"x"`, `{"a":`, ` {"a":[1,2,{"b":null}]} `, "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if s == "" || s == "-" || strings.HasPrefix(s, "@") {
			return // those name no document: {} and the other readers
		}
		for _, got := range []func() (json.RawMessage, error){
			func() (json.RawMessage, error) { return ReadPayload(s, strings.NewReader("")) },
			func() (json.RawMessage, error) { return ReadPayload("-", strings.NewReader(s)) },
		} {
			p, err := got()
			if err != nil {
				if p != nil {
					t.Fatalf("a payload and an error: %q, %v", p, err)
				}
				continue
			}
			if !bytes.Equal(p, []byte(s)) {
				t.Fatalf("changed: %q became %q", s, p)
			}
			var m map[string]any
			if err := json.Unmarshal(p, &m); err != nil || m == nil {
				t.Fatalf("accepted something other than an object: %q (%v)", s, err)
			}
		}
	})
}
