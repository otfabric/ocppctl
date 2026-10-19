// SPDX-License-Identifier: MIT

package csms

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/otfabric/go-ocpp"
	"gopkg.in/yaml.v3"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// Model describes how the CSMS answers its stations. Where OCPP leaves a decision to the
// CSMS (accept a station, accept a token, which transaction id to give), the model decides.
type Model struct {
	// Boot is the answer to BootNotification.
	Boot BootSpec `yaml:"boot" json:"boot"`
	// Stations lists known stations: their password and per-station answers.
	Stations []StationSpec `yaml:"stations,omitempty" json:"stations,omitempty"`
	// Authorization is the token list Authorize and transactions are checked against.
	Authorization AuthorizationSpec `yaml:"authorization,omitempty" json:"authorization,omitempty"`
	// Transactions configures the OCPP 1.6 transaction ids.
	Transactions TransactionSpec `yaml:"transactions,omitempty" json:"transactions,omitempty"`
	// Actions overrides the answer to individual actions.
	Actions []ActionSpec `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// BootSpec is the answer to BootNotification.
type BootSpec struct {
	// Status is Accepted, Pending or Rejected. Default Accepted.
	Status string `yaml:"status,omitempty" json:"status,omitempty"`
	// Interval is the interval field of the answer, in seconds: the heartbeat interval when
	// Accepted, the retry interval otherwise. Default 300.
	Interval *int `yaml:"interval,omitempty" json:"interval,omitempty"`
}

// StationSpec is one known station.
type StationSpec struct {
	// Identity is the last path segment the station connects with.
	Identity string `yaml:"identity" json:"identity"`
	// Password is its HTTP Basic password (security profiles 1 and 2).
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
	// BootStatus overrides boot.status for this station.
	BootStatus string `yaml:"boot_status,omitempty" json:"boot_status,omitempty"`
}

// AuthorizationSpec is the token list.
type AuthorizationSpec struct {
	// Tokens are the accepted idTag (1.6) or idToken.idToken (2.x) values. Any other is
	// answered Invalid. An empty list accepts every token.
	Tokens []string `yaml:"tokens,omitempty" json:"tokens,omitempty"`
}

// TransactionSpec configures transaction ids.
type TransactionSpec struct {
	// FirstID is the transactionId given to the first OCPP 1.6 StartTransaction; each
	// further one gets the next integer. Default 1.
	FirstID *int `yaml:"first_id,omitempty" json:"first_id,omitempty"`
}

// Answer modes of an action override.
const (
	AnswerResponse     = "response"
	AnswerReject       = "reject"
	AnswerNotSupported = "not-supported"
)

// ActionSpec overrides the answer to one action.
type ActionSpec struct {
	// Action is the OCPP action name, such as DataTransfer.
	Action string `yaml:"action" json:"action"`
	// Versions limits the override to these versions (default: every version).
	Versions []string `yaml:"versions,omitempty" json:"versions,omitempty"`
	// Stations limits the override to these identities (default: every station).
	Stations []string `yaml:"stations,omitempty" json:"stations,omitempty"`
	// Answer is "response" (the payload of Response, the default), "reject" (the payload
	// {"status":"Rejected"}) or "not-supported" (the CALLERROR NotSupported).
	Answer string `yaml:"answer,omitempty" json:"answer,omitempty"`
	// Response is the CALLRESULT payload when Answer is "response".
	Response map[string]any `yaml:"response,omitempty" json:"response,omitempty"`

	versions []ocpp.Version
	payload  json.RawMessage
}

// Boot statuses OCPP defines for BootNotification (1.6 RegistrationStatus, 2.x
// RegistrationStatusEnumType).
var bootStatuses = map[string]bool{"Accepted": true, "Pending": true, "Rejected": true}

// Default interval of the BootNotification answer, in seconds.
const defaultInterval = 300

// Load reads and validates a model file for the versions served.
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

// Parse decodes a model from YAML; JSON is read the same way. A field the model does not
// know is an error, so that a typo does not go unnoticed.
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

// Validate checks the model against the versions served and reports everything that is
// wrong with it at once. It prepares the model for use.
func (m *Model) Validate(versions []ocpp.Version) error {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if m.Boot.Status == "" {
		m.Boot.Status = "Accepted"
	} else if !bootStatuses[m.Boot.Status] {
		bad("boot.status %q: use Accepted, Pending or Rejected", m.Boot.Status)
	}
	if m.Boot.Interval == nil {
		m.Boot.Interval = ptr(defaultInterval)
	} else if *m.Boot.Interval < 0 {
		bad("boot.interval %d: use a number of seconds, 0 or more", *m.Boot.Interval)
	}
	seen := map[string]bool{}
	for i, s := range m.Stations {
		what := fmt.Sprintf("stations[%d]", i)
		switch {
		case strings.TrimSpace(s.Identity) == "":
			bad("%s: identity is required", what)
		case seen[s.Identity]:
			bad("%s: identity %q is listed twice", what, s.Identity)
		}
		seen[s.Identity] = true
		if s.BootStatus != "" && !bootStatuses[s.BootStatus] {
			bad("%s: boot_status %q: use Accepted, Pending or Rejected", what, s.BootStatus)
		}
	}
	for i, t := range m.Authorization.Tokens {
		if strings.TrimSpace(t) == "" {
			bad("authorization.tokens[%d]: empty token", i)
		}
	}
	if m.Transactions.FirstID == nil {
		m.Transactions.FirstID = ptr(1)
	} else if *m.Transactions.FirstID < 1 {
		bad("transactions.first_id %d: use 1 or more", *m.Transactions.FirstID)
	}
	for i := range m.Actions {
		problems = append(problems, m.Actions[i].prepare(fmt.Sprintf("actions[%d]", i), versions)...)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// prepare checks an override and computes its payload; it returns the problems found.
func (a *ActionSpec) prepare(what string, served []ocpp.Version) []string {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if a.Action == "" {
		return []string{what + ": action is required"}
	}
	what += " (" + a.Action + ")"
	a.versions = nil
	if len(a.Versions) == 0 {
		a.versions = append(a.versions, served...)
	} else {
		vs, err := ocppx.ParseVersions(a.Versions)
		if err != nil {
			bad("%s: versions: %v", what, err)
		}
		a.versions = vs
	}
	switch a.Answer {
	case "", AnswerResponse:
		a.Answer = AnswerResponse
		if a.Response == nil {
			bad("%s: response is required (or answer: reject or not-supported)", what)
			return problems
		}
		b, err := json.Marshal(a.Response)
		if err != nil {
			bad("%s: response: %v", what, err)
			return problems
		}
		a.payload = b
	case AnswerReject:
		a.payload = json.RawMessage(`{"status":"Rejected"}`)
	case AnswerNotSupported:
	default:
		bad("%s: answer %q: use response, reject or not-supported", what, a.Answer)
		return problems
	}
	if a.Answer != AnswerResponse && a.Response != nil {
		bad("%s: response goes with answer: response only", what)
	}
	known := false
	for _, v := range a.versions {
		act, err := ocppx.Lookup(v, a.Action)
		if err != nil {
			if len(a.Versions) > 0 {
				bad("%s: %v", what, err)
			}
			continue
		}
		known = true
		if act.From == ocppx.FromCSMS {
			bad("%s: %s is sent by the CSMS, not by a station: there is nothing to answer", what, a.Action)
			continue
		}
		if act.Kind == "send" {
			bad("%s: %s is a SEND, which is never answered", what, a.Action)
			continue
		}
		if a.payload != nil {
			if err := ocppx.ValidateResponse(v, a.Action, a.payload); err != nil {
				bad("%s: the answer is not a valid %s response of OCPP %s: %v", what, a.Action, v, err)
			}
		}
	}
	if !known && len(a.Versions) == 0 {
		bad("%s: not an action of the OCPP versions served", what)
	}
	return problems
}

// override returns the override of action for station id on v, or nil.
func (m *Model) override(v ocpp.Version, id, action string) *ActionSpec {
	for i := range m.Actions {
		a := &m.Actions[i]
		if a.Action != action || !hasVersion(a.versions, v) {
			continue
		}
		if len(a.Stations) > 0 && !contains(a.Stations, id) {
			continue
		}
		return a
	}
	return nil
}

// station returns the entry of identity id, or nil.
func (m *Model) station(id string) *StationSpec {
	for i := range m.Stations {
		if m.Stations[i].Identity == id {
			return &m.Stations[i]
		}
	}
	return nil
}

// bootStatus returns the BootNotification status for station id.
func (m *Model) bootStatus(id string) string {
	if s := m.station(id); s != nil && s.BootStatus != "" {
		return s.BootStatus
	}
	return m.Boot.Status
}

// tokenAccepted reports whether token passes the token list.
func (m *Model) tokenAccepted(token string) bool {
	return len(m.Authorization.Tokens) == 0 || contains(m.Authorization.Tokens, token)
}

// Passwords returns the passwords of the model by identity.
func (m *Model) Passwords() map[string]string {
	out := map[string]string{}
	for _, s := range m.Stations {
		if s.Password != "" {
			out[s.Identity] = s.Password
		}
	}
	return out
}

// Encode returns the model as indented JSON.
func (m *Model) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Default returns the model used without --config: the one generate-config writes.
func Default() *Model {
	m, err := Parse([]byte(DefaultYAML))
	if err != nil {
		panic("csms: the default model does not parse: " + err.Error())
	}
	return m
}

func hasVersion(list []ocpp.Version, v ocpp.Version) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }

// DefaultYAML is the commented model `server generate-config` writes, and the model of a
// server started without --config.
const DefaultYAML = `# ocppctl CSMS model: how "ocppctl server start --config <file>" answers its stations.
# Every field is optional. A field ocppctl does not know is an error, and every problem
# is reported before the server listens. See docs/server-config.md.

# The answer to BootNotification.
boot:
  # Accepted, Pending or Rejected.
  status: Accepted
  # The interval of the answer, in seconds: the heartbeat interval when Accepted, the
  # time before the station retries otherwise.
  interval: 300

# Known stations. A password here (or in --passwords-file) makes the station
# authenticate with HTTP Basic: user name = identity (security profiles 1 and 2). Once
# any password is configured, an identity without one is refused (HTTP 404).
stations: []
#  - identity: CS001
#    password: change-me
#  - identity: CS002
#    boot_status: Pending   # overrides boot.status for this station

# The tokens Authorize, StartTransaction (1.6) and TransactionEvent (2.x) are checked
# against: the idTag (1.6) or idToken.idToken (2.x). Any other token is answered
# Invalid. An empty list accepts every token.
authorization:
  tokens: []
#    - 04A2B3C4D5E6F7
#    - TESTTOKEN

# OCPP 1.6 StartTransaction answers carry an integer transaction id: the first one is
# first_id, each further one the next integer.
transactions:
  first_id: 1

# Overrides of the answer to an action. Without one, the CSMS answers:
#   BootNotification      from "boot" above, with the current time
#   Heartbeat             the current time
#   Authorize             Accepted or Invalid from the token list
#   StartTransaction      (1.6) the next transaction id, idTagInfo from the token list
#   StopTransaction       (1.6) idTagInfo from the token list when an idTag is sent
#   TransactionEvent      (2.x) idTokenInfo from the token list when an idToken is sent
#   StatusNotification, MeterValues and the other notifications: an empty answer
#   DataTransfer          status UnknownVendorId (no vendor extension is implemented)
#   anything else         the first of {} and {"status":"Accepted"} the schema accepts,
#                         or the CALLERROR NotSupported
#
# answer: response (default; give the payload in "response"), reject (the payload
# {"status":"Rejected"}) or not-supported (the CALLERROR NotSupported). "versions" and
# "stations" limit an override; the first one that matches applies. Every response is
# checked against the OCA schema of each version it applies to.
actions: []
#  - action: DataTransfer
#    versions: ["1.6"]
#    response:
#      status: Accepted
#      data: hello
#  - action: SignCertificate
#    answer: not-supported
#  - action: FirmwareStatusNotification
#    stations: [CS002]
#    response: {}
`

// GenerateConfig configures `server generate-config`.
type GenerateConfig struct {
	Output string `flag:"output" env:"OCPPCTL_OUTPUT" desc:"File to write the model to; - for stdout"`
	Format string `flag:"format" desc:"Format of the model: yaml (commented) or json"`
	Force  bool   `flag:"force" desc:"Overwrite an existing --output file"`
}

// Generate returns the default model in format: yaml, with comments, or json.
func Generate(format string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "yaml", "yml":
		return []byte(DefaultYAML), nil
	case "json":
		return Default().Encode()
	}
	return nil, fmt.Errorf("invalid --format %q (expected: yaml, json)", format)
}
