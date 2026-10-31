// SPDX-License-Identifier: MIT

package station

import (
	"testing"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"
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
