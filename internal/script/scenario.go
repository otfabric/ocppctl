// SPDX-License-Identifier: MIT

// Package script runs scenarios (`ocppctl client script`): steps a Charging Station takes
// against a CSMS, with expectations on what the CSMS answers and sends.
package script

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// Scenario is a list of steps.
type Scenario struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Steps   []Step `yaml:"steps"`

	version ocpp.Version // zero: the one the CSMS chooses
}

// Step is one step: exactly one of Call, Send, WaitFor, ExpectNoCall, Sleep, Disconnect
// and Reconnect.
type Step struct {
	Name string `yaml:"name"`

	Call         string   `yaml:"call"`
	Send         string   `yaml:"send"`
	WaitFor      string   `yaml:"wait_for"`
	ExpectNoCall Duration `yaml:"expect_no_call"`
	Sleep        Duration `yaml:"sleep"`
	Disconnect   bool     `yaml:"disconnect"`
	Reconnect    bool     `yaml:"reconnect"`

	// Payload of call and send: an object, or "@file" relative to the scenario.
	Payload Payload `yaml:"payload"`
	// Raw sends the payload of call and send without validating it.
	Raw bool `yaml:"raw"`
	// Expect holds conditions on the response of call, or the request of wait_for.
	Expect map[string]Matcher `yaml:"expect"`
	// ExpectError expects call to be answered with a CALLERROR: its code, or "any".
	ExpectError string `yaml:"expect_error"`
	// Answer is how wait_for answers: an object, "@file", or a behaviour (accept, reject,
	// not-supported, not-implemented). Default: the first schema-valid answer.
	Answer Payload `yaml:"answer"`
	// Save stores fields of the response (call) or request (wait_for) as variables:
	// name: path.
	Save map[string]string `yaml:"save"`
	// Timeout of call and wait_for (default: --timeout).
	Timeout Duration `yaml:"timeout"`
}

// Kind returns the kind of a step, and its action.
func (s *Step) Kind() (kind, action string) {
	switch {
	case s.Call != "":
		return "call", s.Call
	case s.Send != "":
		return "send", s.Send
	case s.WaitFor != "":
		return "wait_for", s.WaitFor
	case s.ExpectNoCall != 0:
		return "expect_no_call", ""
	case s.Sleep != 0:
		return "sleep", ""
	case s.Disconnect:
		return "disconnect", ""
	case s.Reconnect:
		return "reconnect", ""
	}
	return "", ""
}

func (s *Step) kinds() int {
	n := 0
	for _, set := range []bool{s.Call != "", s.Send != "", s.WaitFor != "", s.ExpectNoCall != 0, s.Sleep != 0, s.Disconnect, s.Reconnect} {
		if set {
			n++
		}
	}
	return n
}

// Payload is a JSON object, a file reference "@file", or (for answers) a behaviour.
type Payload struct {
	JSON      json.RawMessage
	File      string
	Behaviour string
}

// IsZero reports whether no payload was given.
func (p Payload) IsZero() bool { return p.JSON == nil && p.File == "" && p.Behaviour == "" }

// UnmarshalYAML accepts a mapping, "@file", or a behaviour name.
func (p *Payload) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode:
		var v map[string]any
		if err := n.Decode(&v); err != nil {
			return err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		p.JSON = b
		return nil
	case yaml.ScalarNode:
		switch {
		case strings.HasPrefix(n.Value, "@"):
			p.File = n.Value[1:]
		case strings.HasPrefix(strings.TrimSpace(n.Value), "{"):
			p.JSON = json.RawMessage(n.Value)
		default:
			p.Behaviour = strings.ToLower(strings.TrimSpace(n.Value))
		}
		return nil
	default:
	}
	return fmt.Errorf("line %d: a payload is an object or \"@file\"", n.Line)
}

// Matcher is a condition on one field: equality with a value, or an operator object
// {regex: ...}, {present: true|false}, {one_of: [...]}.
type Matcher struct {
	Equal   any
	Regex   *regexp.Regexp
	Present *bool
	OneOf   []any
	isOp    bool
}

// UnmarshalYAML accepts a scalar (equality) or an operator mapping.
func (m *Matcher) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		var ops map[string]yaml.Node
		if err := n.Decode(&ops); err != nil {
			return err
		}
		if _, op := ops["regex"]; op || hasKey(ops, "present") || hasKey(ops, "one_of") {
			m.isOp = true
			for k, v := range ops {
				switch k {
				case "regex":
					re, err := regexp.Compile(v.Value)
					if err != nil {
						return fmt.Errorf("line %d: regex %q: %w", v.Line, v.Value, err)
					}
					m.Regex = re
				case "present":
					var b bool
					if err := v.Decode(&b); err != nil {
						return fmt.Errorf("line %d: present is true or false", v.Line)
					}
					m.Present = &b
				case "one_of":
					if err := v.Decode(&m.OneOf); err != nil {
						return fmt.Errorf("line %d: one_of is a list", v.Line)
					}
				default:
					return fmt.Errorf("line %d: unknown condition %q: use regex, present or one_of", v.Line, k)
				}
			}
			return nil
		}
	}
	return n.Decode(&m.Equal)
}

func hasKey(m map[string]yaml.Node, k string) bool {
	_, ok := m[k]
	return ok
}

// Duration is a time.Duration written as "30s".
type Duration time.Duration

// UnmarshalYAML parses a Go duration.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(strings.TrimSpace(n.Value))
	if err != nil || v <= 0 {
		return fmt.Errorf("line %d: %q is not a positive duration such as 30s", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// Load reads and checks a scenario. Payload files are read relative to its directory.
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the operator's own scenario
	if err != nil {
		return nil, err
	}
	s, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if err := s.resolve(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse decodes a scenario; a field it does not know is an error.
func Parse(data []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the scenario is empty")
		}
		return nil, err
	}
	if s.Version != "" {
		v, err := ocppx.ParseVersion(s.Version)
		if err != nil {
			return nil, err
		}
		s.version = v
	}
	return &s, nil
}

var behaviours = map[string]bool{"accept": true, "reject": true, "not-supported": true, "not-implemented": true}

// resolve reads payload files and checks every step, reporting every problem at once.
func (s *Scenario) resolve(dir string) error {
	var problems []string
	bad := func(i int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("steps[%d]: ", i)+fmt.Sprintf(format, args...))
	}
	if len(s.Steps) == 0 {
		problems = append(problems, "steps: a scenario needs at least one step")
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		if st.kinds() != 1 {
			bad(i, "a step is exactly one of call, send, wait_for, expect_no_call, sleep, disconnect, reconnect")
			continue
		}
		kind, action := st.Kind()
		for _, p := range []*Payload{&st.Payload, &st.Answer} {
			if p.File == "" {
				continue
			}
			f := p.File
			if !filepath.IsAbs(f) {
				f = filepath.Join(dir, f)
			}
			b, err := os.ReadFile(f) //nolint:gosec // the operator's own payload file
			if err != nil {
				bad(i, "%v", err)
				continue
			}
			p.JSON = b
		}
		if st.Payload.Behaviour != "" {
			bad(i, "payload %q: give an object or \"@file\"", st.Payload.Behaviour)
		}
		if st.Answer.Behaviour != "" && !behaviours[st.Answer.Behaviour] {
			bad(i, "answer %q: give an object, \"@file\", accept, reject, not-supported or not-implemented", st.Answer.Behaviour)
		}
		for _, p := range []Payload{st.Payload, st.Answer} {
			if p.JSON != nil && !json.Valid(p.JSON) {
				bad(i, "the payload is not valid JSON")
			}
		}
		switch kind {
		case "call", "send":
			if !st.Answer.IsZero() {
				bad(i, "answer belongs to wait_for")
			}
		case "wait_for":
			if !st.Payload.IsZero() || st.ExpectError != "" {
				bad(i, "payload and expect_error belong to call and send")
			}
		default:
			if !st.Payload.IsZero() || !st.Answer.IsZero() || len(st.Expect) > 0 || st.ExpectError != "" || len(st.Save) > 0 {
				bad(i, "%s takes no payload, answer, expect, expect_error or save", kind)
			}
		}
		if kind == "send" && (len(st.Expect) > 0 || st.ExpectError != "" || len(st.Save) > 0) {
			bad(i, "a SEND is never answered: it takes no expect, expect_error or save")
		}
		if st.ExpectError != "" && len(st.Expect) > 0 {
			bad(i, "expect and expect_error exclude each other")
		}
		if s.version != 0 && action != "" {
			a, err := ocppx.Lookup(s.version, action)
			switch {
			case err != nil && !st.Raw:
				bad(i, "%v", err)
			case err != nil:
			case kind == "wait_for" && a.From == ocppx.FromStation:
				bad(i, "%s is sent by the station: the CSMS never sends it", action)
			case kind != "wait_for" && a.From == ocppx.FromCSMS && !st.Raw:
				bad(i, "%s is sent by the CSMS, not by a station", action)
			case kind == "call" && a.Kind == "send":
				bad(i, "%s is a SEND: use send", action)
			case kind == "send" && a.Kind != "send" && !st.Raw:
				bad(i, "%s is a CALL: use call", action)
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}
