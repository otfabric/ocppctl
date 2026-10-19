// SPDX-License-Identifier: MIT

// Package types holds the results ocppctl writes to stdout. Each has a JSON form (the
// field names are part of the contract: docs/json-output.md), a text form and a table form.
package types

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Received is a CALL or SEND the peer sent while ocppctl did something else.
type Received struct {
	Time      Millis          `json:"time"`
	Action    string          `json:"action"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

// CallResult is the result of a CALL ocppctl sent and the peer answered with a CALLRESULT.
type CallResult struct {
	Target     string          `json:"target"`
	Version    string          `json:"version"`
	Action     string          `json:"action"`
	Request    json.RawMessage `json:"request"`
	Response   json.RawMessage `json:"response"`
	DurationMS int64           `json:"duration_ms"`
	Validated  bool            `json:"validated"`
	Received   []Received      `json:"received"`
}

// MarshalTextOutput renders the response, indented, with a header line.
func (r CallResult) MarshalTextOutput() (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s answered (OCPP %s, %d ms)\n", r.Action, r.Version, r.DurationMS)
	b.WriteString(indentJSON(r.Response))
	for _, rc := range r.Received {
		_, _ = fmt.Fprintf(&b, "\nreceived %s from the peer, answered %s\n", rc.Action, answered(rc))
	}
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler: one row per top-level response field.
func (r CallResult) TableHeaders() []string { return []string{"FIELD", "VALUE"} }

// TableRows implements format.TableMarshaler.
func (r CallResult) TableRows() [][]string { return fieldRows(r.Response) }

// SendResult is the result of a SEND (OCPP 2.1): queued, never answered.
type SendResult struct {
	Target  string          `json:"target"`
	Version string          `json:"version"`
	Action  string          `json:"action"`
	Request json.RawMessage `json:"request"`
	Sent    bool            `json:"sent"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r SendResult) MarshalTextOutput() (string, error) {
	return fmt.Sprintf("%s sent as a SEND (OCPP %s): it is never answered", r.Action, r.Version), nil
}

// TableHeaders implements format.TableMarshaler.
func (r SendResult) TableHeaders() []string { return []string{"ACTION", "VERSION", "SENT"} }

// TableRows implements format.TableMarshaler.
func (r SendResult) TableRows() [][]string {
	return [][]string{{r.Action, r.Version, strconv.FormatBool(r.Sent)}}
}

// TLSInfo describes a TLS connection.
type TLSInfo struct {
	Version     string    `json:"version"`
	CipherSuite string    `json:"cipher_suite"`
	ServerName  string    `json:"server_name,omitempty"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotAfter    time.Time `json:"not_after"`
	ClientCert  bool      `json:"client_certificate"`
}

// ProbeAttempt is one connection attempt of a probe.
type ProbeAttempt struct {
	Offered     []string   `json:"offered"`
	Negotiated  string     `json:"negotiated,omitempty"`
	OK          bool       `json:"ok"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	Compression bool       `json:"compression"`
	HandshakeMS int64      `json:"handshake_ms"`
	Auth        string     `json:"authentication"`
	TLS         *TLSInfo   `json:"tls,omitempty"`
	Error       *ErrorInfo `json:"error,omitempty"`
}

// ProbeResult is what a probe found out about a CSMS.
type ProbeResult struct {
	Target   string         `json:"target"`
	Identity string         `json:"identity"`
	Attempts []ProbeAttempt `json:"attempts"`
	// Accepted lists the versions the CSMS accepted.
	Accepted []string `json:"accepted"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r ProbeResult) MarshalTextOutput() (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "target          %s\nidentity        %s\n", r.Target, r.Identity)
	for i, a := range r.Attempts {
		if len(r.Attempts) > 1 {
			_, _ = fmt.Fprintf(&b, "\nattempt %d\n", i+1)
		}
		_, _ = fmt.Fprintf(&b, "offered         %s\n", strings.Join(a.Offered, ", "))
		if a.OK {
			_, _ = fmt.Fprintf(&b, "negotiated      %s\n", a.Negotiated)
		} else if a.Error != nil {
			_, _ = fmt.Fprintf(&b, "refused         %s\n", a.Error.Message)
		}
		if a.TLS != nil {
			_, _ = fmt.Fprintf(&b, "tls             %s, %s, certificate %s (issuer %s, valid until %s)%s\n", a.TLS.Version, a.TLS.CipherSuite,
				a.TLS.Subject, a.TLS.Issuer, a.TLS.NotAfter.Format("2006-01-02"), map[bool]string{true: ", client certificate sent"}[a.TLS.ClientCert])
		}
		_, _ = fmt.Fprintf(&b, "authentication  %s\n", a.Auth)
		if a.OK {
			_, _ = fmt.Fprintf(&b, "compression     %s\n", map[bool]string{true: "negotiated", false: "not negotiated"}[a.Compression])
			_, _ = fmt.Fprintf(&b, "handshake       %d ms\n", a.HandshakeMS)
		}
	}
	if len(r.Attempts) > 1 {
		_, _ = fmt.Fprintf(&b, "\naccepted        %s\n", orNone(strings.Join(r.Accepted, ", ")))
	}
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r ProbeResult) TableHeaders() []string {
	return []string{"OFFERED", "RESULT", "VERSION", "TLS", "AUTH", "COMPRESSION", "HANDSHAKE_MS"}
}

// TableRows implements format.TableMarshaler.
func (r ProbeResult) TableRows() [][]string {
	var rows [][]string
	for _, a := range r.Attempts {
		result := "accepted"
		if !a.OK {
			result = "refused"
			if a.HTTPStatus != 0 {
				result = "HTTP " + strconv.Itoa(a.HTTPStatus)
			} else if a.Error != nil {
				result = a.Error.Code
			}
		}
		tlsv := "-"
		if a.TLS != nil {
			tlsv = a.TLS.Version
		}
		rows = append(rows, []string{strings.Join(a.Offered, ","), result, orDash(a.Negotiated), tlsv, a.Auth,
			strconv.FormatBool(a.Compression), strconv.FormatInt(a.HandshakeMS, 10)})
	}
	return rows
}

// ActionInfo is one action of a version.
type ActionInfo struct {
	Name string `json:"name"`
	From string `json:"from"`
	Kind string `json:"kind"`
}

// ActionsResult lists the actions of a version.
type ActionsResult struct {
	Version string       `json:"version"`
	Actions []ActionInfo `json:"actions"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r ActionsResult) MarshalTextOutput() (string, error) {
	var b strings.Builder
	for _, a := range r.Actions {
		k := ""
		if a.Kind == "send" {
			k = " (SEND)"
		}
		_, _ = fmt.Fprintf(&b, "%-36s sent by %s%s\n", a.Name, a.From, k)
	}
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r ActionsResult) TableHeaders() []string { return []string{"ACTION", "FROM", "KIND"} }

// TableRows implements format.TableMarshaler.
func (r ActionsResult) TableRows() [][]string {
	rows := make([][]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		rows = append(rows, []string{a.Name, a.From, a.Kind})
	}
	return rows
}

// Violation is one schema violation.
type Violation struct {
	Field     string `json:"field,omitempty"`
	Reason    string `json:"reason"`
	ErrorCode string `json:"error_code"`
}

// ValidateResult is the result of a validation.
type ValidateResult struct {
	Version    string      `json:"version"`
	Action     string      `json:"action"`
	Message    string      `json:"message"`
	Valid      bool        `json:"valid"`
	Violations []Violation `json:"violations"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r ValidateResult) MarshalTextOutput() (string, error) {
	if r.Valid {
		return fmt.Sprintf("valid: %s %s of OCPP %s", r.Action, r.Message, r.Version), nil
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "invalid: %s %s of OCPP %s\n", r.Action, r.Message, r.Version)
	for _, v := range r.Violations {
		field := v.Field
		if field == "" {
			field = "(payload)"
		}
		_, _ = fmt.Fprintf(&b, "  %s: %s (%s)\n", field, v.Reason, v.ErrorCode)
	}
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r ValidateResult) TableHeaders() []string { return []string{"FIELD", "REASON", "ERROR_CODE"} }

// TableRows implements format.TableMarshaler.
func (r ValidateResult) TableRows() [][]string {
	rows := [][]string{}
	for _, v := range r.Violations {
		rows = append(rows, []string{orDash(v.Field), v.Reason, v.ErrorCode})
	}
	if r.Valid {
		rows = append(rows, []string{"-", "valid", "-"})
	}
	return rows
}

// FrameResult is a decoded or encoded OCPP-J frame.
type FrameResult struct {
	Version     string          `json:"version"`
	Type        string          `json:"type"`
	TypeNumber  int             `json:"type_number"`
	MessageID   string          `json:"message_id"`
	Action      string          `json:"action,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
	Frame       string          `json:"frame"`
	// PayloadValid is set when the payload of a CALL or SEND was checked against the schema.
	PayloadValid *bool      `json:"payload_valid,omitempty"`
	Violation    *Violation `json:"violation,omitempty"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r FrameResult) MarshalTextOutput() (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "type        %s (%d)\nmessage id  %s\n", r.Type, r.TypeNumber, r.MessageID)
	if r.Action != "" {
		_, _ = fmt.Fprintf(&b, "action      %s\n", r.Action)
	}
	if r.ErrorCode != "" {
		_, _ = fmt.Fprintf(&b, "error code  %s\ndescription %s\n", r.ErrorCode, r.Description)
	}
	if r.PayloadValid != nil {
		v := "valid"
		if !*r.PayloadValid {
			v = "invalid"
			if r.Violation != nil {
				v += ": " + orDash(r.Violation.Field) + ": " + r.Violation.Reason
			}
		}
		_, _ = fmt.Fprintf(&b, "payload     %s for OCPP %s\n", v, r.Version)
	}
	if len(r.Payload) > 0 {
		b.WriteString(indentJSON(r.Payload))
	}
	if len(r.Details) > 0 && string(r.Details) != "{}" {
		b.WriteString(indentJSON(r.Details))
	}
	_, _ = fmt.Fprintf(&b, "frame       %s\n", r.Frame)
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r FrameResult) TableHeaders() []string {
	return []string{"TYPE", "MESSAGE_ID", "ACTION", "ERROR_CODE", "FRAME"}
}

// TableRows implements format.TableMarshaler.
func (r FrameResult) TableRows() [][]string {
	return [][]string{{r.Type, r.MessageID, orDash(r.Action), orDash(r.ErrorCode), r.Frame}}
}

// CertHashResult is the CertificateHashData of a certificate.
type CertHashResult struct {
	Certificate    string `json:"certificate"`
	HashAlgorithm  string `json:"hash_algorithm"`
	IssuerNameHash string `json:"issuer_name_hash"`
	IssuerKeyHash  string `json:"issuer_key_hash"`
	SerialNumber   string `json:"serial_number"`
	// OCPP is the certificateHashData object as OCPP messages carry it.
	OCPP map[string]string `json:"ocpp"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r CertHashResult) MarshalTextOutput() (string, error) {
	return fmt.Sprintf("certificate       %s\nhash algorithm    %s\nissuer name hash  %s\nissuer key hash   %s\nserial number     %s",
		r.Certificate, r.HashAlgorithm, r.IssuerNameHash, r.IssuerKeyHash, r.SerialNumber), nil
}

// TableHeaders implements format.TableMarshaler.
func (r CertHashResult) TableHeaders() []string {
	return []string{"HASH_ALGORITHM", "ISSUER_NAME_HASH", "ISSUER_KEY_HASH", "SERIAL_NUMBER"}
}

// TableRows implements format.TableMarshaler.
func (r CertHashResult) TableRows() [][]string {
	return [][]string{{r.HashAlgorithm, r.IssuerNameHash, r.IssuerKeyHash, r.SerialNumber}}
}

func answered(r Received) string {
	if r.ErrorCode != "" {
		return "CALLERROR " + r.ErrorCode
	}
	if len(r.Response) == 0 {
		return "nothing (a SEND)"
	}
	return string(r.Response)
}

func indentJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw) + "\n"
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b) + "\n"
}

// fieldRows flattens the top level of a JSON object into FIELD/VALUE rows.
func fieldRows(raw json.RawMessage) [][]string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return [][]string{{"(response)", string(raw)}}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		v := string(m[k])
		var s string
		if json.Unmarshal(m[k], &s) == nil {
			v = s
		}
		rows = append(rows, []string{k, v})
	}
	return rows
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func sortStrings(s []string) { sort.Strings(s) }

// PayloadResult is a request built without sending it (--payload-only).
type PayloadResult struct {
	Version string          `json:"version"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

// MarshalTextOutput prints the payload alone, so that it can be piped into --payload -.
func (r PayloadResult) MarshalTextOutput() (string, error) { return indentJSON(r.Payload), nil }

// TableHeaders implements format.TableMarshaler.
func (r PayloadResult) TableHeaders() []string { return []string{"FIELD", "VALUE"} }

// TableRows implements format.TableMarshaler.
func (r PayloadResult) TableRows() [][]string { return fieldRows(r.Payload) }

// StationEvent is one event of a simulated station (`client run`), streamed as it happens.
type StationEvent struct {
	Time    Millis `json:"time"`
	Station string `json:"station"`
	// Event is connected, disconnected, reconnecting, booted, boot_pending,
	// boot_rejected, call_sent, call_result, call_error, call_received, answered,
	// send_received, session_started, session_ended, reset or error.
	Event       string          `json:"event"`
	Version     string          `json:"version,omitempty"`
	Action      string          `json:"action,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
	Response    json.RawMessage `json:"response,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	DurationMS  *int64          `json:"duration_ms,omitempty"`
	Transaction string          `json:"transaction,omitempty"`
	Message     string          `json:"message,omitempty"`
}

// MarshalTextOutput renders one line.
func (e StationEvent) MarshalTextOutput() (string, error) {
	return fmt.Sprintf("%s %s %s %s", e.Time.Format("15:04:05.000"), e.Station, e.Event, e.detail()), nil
}

func (e StationEvent) detail() string {
	var parts []string
	if e.Action != "" {
		parts = append(parts, e.Action)
	}
	if e.Transaction != "" {
		parts = append(parts, "transaction "+e.Transaction)
	}
	if e.ErrorCode != "" {
		parts = append(parts, e.ErrorCode)
	}
	if e.DurationMS != nil {
		parts = append(parts, strconv.FormatInt(*e.DurationMS, 10)+" ms")
	}
	switch {
	case len(e.Response) > 0:
		parts = append(parts, string(e.Response))
	case len(e.Request) > 0:
		parts = append(parts, string(e.Request))
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, " ")
}

// TableHeaders implements format.TableMarshaler.
func (e StationEvent) TableHeaders() []string { return []string{"TIME", "STATION", "EVENT", "DETAIL"} }

// TableRows implements format.TableMarshaler.
func (e StationEvent) TableRows() [][]string {
	return [][]string{{e.Time.Format("15:04:05.000"), e.Station, e.Event, orDash(e.detail())}}
}

// StepResult is the outcome of one step of a scenario.
type StepResult struct {
	Index      int             `json:"index"`
	Name       string          `json:"name,omitempty"`
	Kind       string          `json:"kind"`
	Action     string          `json:"action,omitempty"`
	Result     string          `json:"result"` // pass, fail or skipped
	DurationMS int64           `json:"duration_ms"`
	Message    string          `json:"message,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
}

// ScriptResult is the outcome of a scenario (`client script`).
type ScriptResult struct {
	Scenario string       `json:"scenario"`
	Target   string       `json:"target"`
	Version  string       `json:"version"`
	Passed   int          `json:"passed"`
	Failed   int          `json:"failed"`
	Skipped  int          `json:"skipped"`
	Steps    []StepResult `json:"steps"`
	// Unexpected lists the CALLs from the CSMS that no step waited for; they were answered
	// with the default answer.
	Unexpected []Received `json:"unexpected"`
	DurationMS int64      `json:"duration_ms"`
}

// MarshalTextOutput renders one line per step and a summary.
func (r ScriptResult) MarshalTextOutput() (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "scenario %s against %s (OCPP %s)\n", r.Scenario, r.Target, r.Version)
	for _, s := range r.Steps {
		label := s.Kind
		if s.Action != "" {
			label += " " + s.Action
		}
		if s.Name != "" {
			label = s.Name + ": " + label
		}
		_, _ = fmt.Fprintf(&b, "%-7s %2d  %s (%d ms)", strings.ToUpper(s.Result), s.Index, label, s.DurationMS)
		if s.Message != "" {
			_, _ = fmt.Fprintf(&b, ": %s", s.Message)
		}
		b.WriteString("\n")
	}
	for _, u := range r.Unexpected {
		_, _ = fmt.Fprintf(&b, "note       received %s, which no step waited for; answered %s\n", u.Action, answered(u))
	}
	_, _ = fmt.Fprintf(&b, "%d passed, %d failed, %d skipped in %d ms\n", r.Passed, r.Failed, r.Skipped, r.DurationMS)
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r ScriptResult) TableHeaders() []string {
	return []string{"STEP", "NAME", "KIND", "ACTION", "RESULT", "MS", "MESSAGE"}
}

// TableRows implements format.TableMarshaler.
func (r ScriptResult) TableRows() [][]string {
	rows := make([][]string, 0, len(r.Steps))
	for _, s := range r.Steps {
		rows = append(rows, []string{strconv.Itoa(s.Index), orDash(s.Name), s.Kind, orDash(s.Action), s.Result,
			strconv.FormatInt(s.DurationMS, 10), orDash(s.Message)})
	}
	return rows
}

// Millis is a time written as RFC 3339 in UTC with milliseconds, as every time in
// ocppctl's JSON output.
type Millis time.Time

// MarshalJSON implements json.Marshaler.
func (m Millis) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(m).UTC().Format("2006-01-02T15:04:05.000Z07:00"))
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *Millis) UnmarshalJSON(b []byte) error {
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	*m = Millis(t)
	return nil
}

// Format formats the time as time.Time does.
func (m Millis) Format(layout string) string { return time.Time(m).Format(layout) }

// IsZero reports whether the time is the zero time.
func (m Millis) IsZero() bool { return time.Time(m).IsZero() }
