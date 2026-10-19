// SPDX-License-Identifier: MIT

// Package station is the simulated Charging Station of `ocppctl client run`: its model
// (what it is, how it answers the CSMS, which sessions it charges) and its runtime.
package station

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// Model describes a simulated station.
type Model struct {
	Station       Info                 `yaml:"station" json:"station"`
	EVSEs         []EVSE               `yaml:"evses" json:"evses"`
	Heartbeat     Duration             `yaml:"heartbeat_interval,omitempty" json:"heartbeat_interval,omitempty"`
	MeterInterval Duration             `yaml:"meter_interval,omitempty" json:"meter_interval,omitempty"`
	RemotePowerW  float64              `yaml:"remote_power_w,omitempty" json:"remote_power_w,omitempty"`
	Sessions      []Session            `yaml:"sessions,omitempty" json:"sessions,omitempty"`
	DeviceModel   map[string]Variable  `yaml:"device_model,omitempty" json:"device_model,omitempty"`
	Configuration map[string]ConfigKey `yaml:"configuration,omitempty" json:"configuration,omitempty"`
	Answers       map[string]Answer    `yaml:"answers,omitempty" json:"answers,omitempty"`
}

// Info is what the station says about itself in its BootNotification.
type Info struct {
	Vendor   string `yaml:"vendor" json:"vendor"`
	Model    string `yaml:"model" json:"model"`
	Serial   string `yaml:"serial,omitempty" json:"serial,omitempty"`
	Firmware string `yaml:"firmware,omitempty" json:"firmware,omitempty"`
}

// EVSE is one EVSE and its connectors. OCPP 1.6 has no EVSEs: its connectors are numbered
// across the station in the order the model lists them.
type EVSE struct {
	ID         int   `yaml:"id" json:"id"`
	Connectors []int `yaml:"connectors" json:"connectors"`
}

// Session is a charging session the station starts by itself.
type Session struct {
	After         Duration `yaml:"after" json:"after"`
	EVSE          int      `yaml:"evse" json:"evse"`
	Connector     int      `yaml:"connector" json:"connector"`
	IDToken       string   `yaml:"id_token" json:"id_token"`
	TokenType     string   `yaml:"token_type,omitempty" json:"token_type,omitempty"`
	PowerW        float64  `yaml:"power_w" json:"power_w"`
	MeterInterval Duration `yaml:"meter_interval,omitempty" json:"meter_interval,omitempty"`
	Duration      Duration `yaml:"duration" json:"duration"`
}

// Variable is a device model variable (OCPP 2.x GetVariables, SetVariables), named
// "Component.Variable" in the model.
type Variable struct {
	Value      string `yaml:"value" json:"value"`
	Mutability string `yaml:"mutability,omitempty" json:"mutability,omitempty"`
}

// ConfigKey is a configuration key (OCPP 1.6 GetConfiguration, ChangeConfiguration).
type ConfigKey struct {
	Value    string `yaml:"value" json:"value"`
	Readonly bool   `yaml:"readonly,omitempty" json:"readonly,omitempty"`
}

// Answer is how the station answers an action: a behaviour (accept, reject,
// not-supported, not-implemented) or a static response object.
type Answer struct {
	Behaviour string
	Static    json.RawMessage
}

// Behaviours of an Answer.
const (
	Accept         = "accept"
	Reject         = "reject"
	NotSupported   = "not-supported"
	NotImplemented = "not-implemented"
)

// UnmarshalYAML accepts a behaviour name or a mapping.
func (a *Answer) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		a.Behaviour = strings.ToLower(strings.TrimSpace(n.Value))
		return nil
	case yaml.MappingNode:
		var v map[string]any
		if err := n.Decode(&v); err != nil {
			return err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		a.Static = b
		return nil
	default:
	}
	return fmt.Errorf("line %d: an answer is accept, reject, not-supported, not-implemented or a response object", n.Line)
}

// MarshalYAML writes the behaviour or the object.
func (a Answer) MarshalYAML() (any, error) {
	if a.Static != nil {
		var v any
		err := json.Unmarshal(a.Static, &v)
		return v, err
	}
	return a.Behaviour, nil
}

// MarshalJSON writes the behaviour or the object.
func (a Answer) MarshalJSON() ([]byte, error) {
	if a.Static != nil {
		return a.Static, nil
	}
	return json.Marshal(a.Behaviour)
}

// Duration is a time.Duration written as "30s".
type Duration time.Duration

// UnmarshalYAML parses a Go duration.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(strings.TrimSpace(n.Value))
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration such as 30s or 10m", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes the duration as text.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// MarshalJSON writes the duration as text.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// D returns the time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// Load reads a model from a YAML or JSON file and validates it for the versions given.
func Load(path string, versions []ocpp.Version) (*Model, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the operator's own model file
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.Validate(versions); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse decodes a model from YAML; JSON is a subset of YAML and is read the same way. A
// field the model does not know is an error, so that a typo does not go unnoticed.
func Parse(data []byte) (*Model, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Model
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the model is empty")
		}
		return nil, err
	}
	return &m, nil
}

// Demo returns the built-in model.
func Demo() *Model {
	m, err := Parse([]byte(DemoYAML))
	if err != nil {
		panic("station: the built-in model does not parse: " + err.Error())
	}
	return m
}

var mutabilities = map[string]bool{"": true, "ReadOnly": true, "ReadWrite": true, "WriteOnly": true}

// Validate checks the model and reports everything that is wrong with it at once.
func (m *Model) Validate(versions []ocpp.Version) error {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if m.Station.Vendor == "" || m.Station.Model == "" {
		bad("station: vendor and model are required")
	}
	// 1.6 allows 20 characters for both; 2.x 50 for the vendor and 20 for the model.
	maxVendor := 50
	for _, v := range versions {
		if v == ocpp.V16 {
			maxVendor = 20
		}
	}
	if len(m.Station.Vendor) > maxVendor || len(m.Station.Model) > 20 {
		bad("station: vendor is at most %d characters and model at most 20 for OCPP %s", maxVendor, versionList(versions))
	}
	if len(m.EVSEs) == 0 {
		bad("evses: a station needs at least one EVSE")
	}
	seenEVSE := map[int]bool{}
	for i, e := range m.EVSEs {
		if e.ID <= 0 {
			bad("evses[%d]: id %d: EVSE ids start at 1", i, e.ID)
		}
		if seenEVSE[e.ID] {
			bad("evses[%d]: id %d is used twice", i, e.ID)
		}
		seenEVSE[e.ID] = true
		if len(e.Connectors) == 0 {
			bad("evses[%d]: an EVSE needs at least one connector", i)
		}
		seen := map[int]bool{}
		for _, c := range e.Connectors {
			if c <= 0 || seen[c] {
				bad("evses[%d]: connector %d: connector ids start at 1 and are unique per EVSE", i, c)
			}
			seen[c] = true
		}
	}
	if d := m.Heartbeat.D(); d < 0 || (d > 0 && d < time.Second) {
		bad("heartbeat_interval %s: use at least 1s, or leave it out to use the CSMS's interval", d)
	}
	if d := m.MeterInterval.D(); d < 0 || (d > 0 && d < time.Second) {
		bad("meter_interval %s: use at least 1s", d)
	}
	for i, s := range m.Sessions {
		what := fmt.Sprintf("sessions[%d]", i)
		if !m.hasConnector(s.EVSE, s.Connector) {
			bad("%s: EVSE %d has no connector %d", what, s.EVSE, s.Connector)
		}
		if s.IDToken == "" {
			bad("%s: id_token is required", what)
		}
		if s.PowerW < 0 {
			bad("%s: power_w must not be negative", what)
		}
		if s.After.D() < 0 || s.Duration.D() <= 0 {
			bad("%s: after must not be negative and duration must be positive", what)
		}
		if d := s.MeterInterval.D(); d < 0 || (d > 0 && d < time.Second) {
			bad("%s: meter_interval %s: use at least 1s", what, d)
		}
	}
	for name, v := range m.DeviceModel {
		if c, vr, ok := strings.Cut(name, "."); !ok || c == "" || vr == "" {
			bad("device_model: %q: name variables Component.Variable", name)
		}
		if !mutabilities[v.Mutability] {
			bad("device_model: %q: mutability %q: use ReadOnly, ReadWrite or WriteOnly", name, v.Mutability)
		}
	}
	for _, action := range sortedKeys(m.Answers) {
		a := m.Answers[action]
		m.validateAnswer(action, a, versions, bad)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// validateAnswer checks an answer: the action must be one a CSMS sends in some OCPP
// version (a model may serve several), and a static response must be valid in at least one
// of the offered versions that has the action.
func (m *Model) validateAnswer(action string, a Answer, versions []ocpp.Version, bad func(string, ...any)) {
	known, offered, valid := false, false, false
	for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
		x, err := ocppx.Lookup(v, action)
		if err != nil || x.Name != action {
			continue
		}
		if x.From == ocppx.FromStation {
			bad("answers: %s is sent by the station, not answered by it", action)
			return
		}
		known = true
		for _, o := range versions {
			if o == v {
				offered = true
				valid = valid || (a.Static != nil && ocppx.ValidateResponse(v, action, a.Static) == nil)
			}
		}
	}
	switch {
	case !known:
		bad("answers: %s is not an action a CSMS sends in any OCPP version", action)
	case a.Static != nil && offered && !valid:
		bad("answers: %s: the response is not valid for any of OCPP %s", action, versionList(versions))
	case a.Static == nil && a.Behaviour != Accept && a.Behaviour != Reject && a.Behaviour != NotSupported && a.Behaviour != NotImplemented:
		bad("answers: %s: %q: use accept, reject, not-supported, not-implemented or a response object", action, a.Behaviour)
	}
}

func (m *Model) hasConnector(evse, connector int) bool {
	for _, e := range m.EVSEs {
		if e.ID != evse {
			continue
		}
		for _, c := range e.Connectors {
			if c == connector {
				return true
			}
		}
	}
	return false
}

// connector16 is the OCPP 1.6 connector id of an EVSE's connector: connectors numbered
// across the station in model order.
func (m *Model) connector16(evse, connector int) int {
	n := 0
	for _, e := range m.EVSEs {
		for _, c := range e.Connectors {
			n++
			if e.ID == evse && c == connector {
				return n
			}
		}
	}
	return 0
}

// from16 is the EVSE and connector of an OCPP 1.6 connector id.
func (m *Model) from16(id int) (evse, connector int, ok bool) {
	n := 0
	for _, e := range m.EVSEs {
		for _, c := range e.Connectors {
			n++
			if n == id {
				return e.ID, c, true
			}
		}
	}
	return 0, 0, false
}

func versionList(vs []ocpp.Version) string {
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = v.String()
	}
	return strings.Join(s, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Encode writes the model as JSON.
func (m *Model) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// DemoYAML is the built-in model, with comments; `client generate-config` writes it.
const DemoYAML = `# ocppctl simulated Charging Station: "ocppctl client run --config <this file>".
# YAML or JSON; a field ocppctl does not know is an error. See docs/station-model.md.

# What the station says about itself in its BootNotification.
station:
  vendor: OT Fabric
  model: Simulator
  serial: SIM-0001
  firmware: 1.0.0

# EVSEs and their connectors. OCPP 1.6 has no EVSEs: its connectors are numbered across
# the station in this order (here 1 and 2).
evses:
  - id: 1
    connectors: [1]
  - id: 2
    connectors: [1]

# Heartbeat interval. Leave it out to use the interval the CSMS gives in its
# BootNotification response (or a ChangeConfiguration / SetVariables of it).
# heartbeat_interval: 300s

# How often a running session reports its meter (default 60s), and the power of a session
# the CSMS starts remotely (default 11000 W).
meter_interval: 60s
remote_power_w: 11000

# Charging sessions the station starts by itself, counted from its first accepted boot.
sessions:
  - after: 30s
    evse: 1
    connector: 1
    id_token: 04A1B2C3D4E5F6
    token_type: ISO14443        # OCPP 2.x
    power_w: 11000
    meter_interval: 30s
    duration: 5m

# OCPP 2.x device model, for GetVariables, SetVariables and GetBaseReport:
# "Component.Variable": {value, mutability: ReadOnly | ReadWrite | WriteOnly}.
device_model:
  OCPPCommCtrlr.HeartbeatInterval: {value: "300", mutability: ReadWrite}
  OCPPCommCtrlr.NetworkProfileConnectionAttempts: {value: "3", mutability: ReadWrite}
  AuthCtrlr.AuthorizeRemoteStart: {value: "false", mutability: ReadWrite}
  SecurityCtrlr.SecurityProfile: {value: "1", mutability: ReadOnly}
  ChargingStation.Model: {value: "Simulator", mutability: ReadOnly}

# OCPP 1.6 configuration keys, for GetConfiguration and ChangeConfiguration.
configuration:
  HeartbeatInterval: {value: "300"}
  MeterValueSampleInterval: {value: "60"}
  NumberOfConnectors: {value: "2", readonly: true}
  AuthorizeRemoteTxRequests: {value: "false"}

# How to answer what the CSMS sends: accept, reject, not-supported, not-implemented, or
# the response object itself. An action without an entry gets the station's built-in
# behaviour (see docs/station-model.md), else an empty object or status Accepted,
# whichever its schema allows.
answers:
  UnlockConnector: {status: Unlocked}
  # UpdateFirmware: reject
  # DataTransfer: {status: UnknownVendorId}
  # ReserveNow: not-supported
`
