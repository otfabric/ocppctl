// SPDX-License-Identifier: MIT

package station

import (
	"strings"
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
