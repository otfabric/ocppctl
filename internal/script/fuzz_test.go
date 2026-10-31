// SPDX-License-Identifier: MIT

package script

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse feeds scenario documents, which come from files an operator may
// have been handed. A document that resolves must describe steps the runner
// can dispatch: exactly one kind each, and valid JSON payloads.
func FuzzParse(f *testing.F) {
	paths, err := filepath.Glob("../../examples/scenarios/*.yaml")
	if err != nil || len(paths) == 0 {
		f.Fatalf("no seeds: %v", err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	for _, s := range []string{"", "steps: 1", "name: x\nversion: nope\n", "steps:\n- call: Heartbeat\n  timeout: 1h\n", "a: &a [*a]\n"} {
		f.Add([]byte(s))
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := Parse(data)
		if err != nil {
			if s != nil {
				t.Fatalf("a scenario and an error: %v", err)
			}
			return
		}
		if s == nil {
			t.Fatal("no scenario and no error")
		}
		for _, st := range s.Steps {
			if st.Payload.File != "" || st.Answer.File != "" {
				return // resolve would read whatever path the input names
			}
		}
		if s.resolve(dir) != nil {
			return
		}
		for i, st := range s.Steps {
			if st.kinds() != 1 {
				t.Fatalf("steps[%d]: resolved with %d kinds", i, st.kinds())
			}
			if kind, _ := st.Kind(); kind == "" {
				t.Fatalf("steps[%d]: resolved without a kind", i)
			}
			for _, p := range []Payload{st.Payload, st.Answer} {
				if p.JSON != nil && !json.Valid(p.JSON) {
					t.Fatalf("steps[%d]: resolved with invalid JSON %q", i, p.JSON)
				}
			}
		}
	})
}
