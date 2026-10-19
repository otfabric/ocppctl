// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeLayout is how ocppctl writes times: RFC 3339, UTC, with milliseconds.
const TimeLayout = "2006-01-02T15:04:05.000Z07:00"

// ServerEvent is one record of the event stream of `server start`. Which fields are set
// depends on Event; the others are omitted.
type ServerEvent struct {
	Time string `json:"time"`
	// Event is listening, connected, refused, call_received, answered, send_received,
	// call_sent, call_result, disconnected or stopping.
	Event    string `json:"event"`
	Identity string `json:"identity,omitempty"`
	Version  string `json:"version,omitempty"`
	Remote   string `json:"remote,omitempty"`
	// Address, Versions and ControlSocket describe the listener (listening).
	Address       string   `json:"address,omitempty"`
	Versions      []string `json:"versions,omitempty"`
	ControlSocket string   `json:"control_socket,omitempty"`
	// TLS and Compression describe a connection (connected).
	TLS         *bool  `json:"tls,omitempty"`
	Compression *bool  `json:"compression,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	// Action, Request, Response and ErrorCode describe a message.
	Action           string          `json:"action,omitempty"`
	Request          json.RawMessage `json:"request,omitempty"`
	Response         json.RawMessage `json:"response,omitempty"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorDescription string          `json:"error_description,omitempty"`
	DurationMS       *int64          `json:"duration_ms,omitempty"`
	// Reason says why a connection was refused or ended.
	Reason string `json:"reason,omitempty"`
}

// NewServerEvent returns an event stamped with the current time.
func NewServerEvent(event string) ServerEvent {
	return ServerEvent{Time: time.Now().UTC().Format(TimeLayout), Event: event}
}

func (e *ServerEvent) detail() string {
	var parts []string
	add := func(format string, args ...any) { parts = append(parts, fmt.Sprintf(format, args...)) }
	if e.Address != "" {
		add("address=%s", e.Address)
	}
	if len(e.Versions) > 0 {
		add("versions=%s", strings.Join(e.Versions, ","))
	}
	if e.ControlSocket != "" {
		add("control_socket=%s", e.ControlSocket)
	}
	if e.TLS != nil {
		add("tls=%t", *e.TLS)
	}
	if e.Compression != nil {
		add("compression=%t", *e.Compression)
	}
	if len(e.Request) > 0 {
		add("request=%s", compactJSON(e.Request))
	}
	if len(e.Response) > 0 {
		add("response=%s", compactJSON(e.Response))
	}
	if e.ErrorCode != "" {
		add("error=%s", e.ErrorCode)
		if e.ErrorDescription != "" {
			add("description=%q", e.ErrorDescription)
		}
	}
	if e.DurationMS != nil {
		add("duration_ms=%d", *e.DurationMS)
	}
	if e.Reason != "" {
		add("reason=%q", e.Reason)
	}
	return strings.Join(parts, " ")
}

// MarshalTextOutput implements format.TextMarshaler: one line per event.
func (e *ServerEvent) MarshalTextOutput() (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s  %-13s", e.Time, e.Event)
	for _, s := range []string{e.Identity, e.Version, e.Remote, e.Action} {
		if s != "" {
			_, _ = fmt.Fprintf(&b, "  %s", s)
		}
	}
	if d := e.detail(); d != "" {
		_, _ = fmt.Fprintf(&b, "  %s", d)
	}
	return strings.TrimRight(b.String(), " "), nil
}

// TableHeaders implements format.TableMarshaler.
func (e *ServerEvent) TableHeaders() []string {
	return []string{"TIME", "EVENT", "IDENTITY", "VERSION", "REMOTE", "ACTION", "DETAIL"}
}

// TableRows implements format.TableMarshaler.
func (e *ServerEvent) TableRows() [][]string {
	return [][]string{{e.Time, e.Event, orDash(e.Identity), orDash(e.Version), orDash(e.Remote), orDash(e.Action), orDash(e.detail())}}
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// StationInfo is one station connected to a running `server start`.
type StationInfo struct {
	Identity    string `json:"identity"`
	Version     string `json:"version"`
	Remote      string `json:"remote"`
	ConnectedAt Millis `json:"connected_at"`
	TLS         bool   `json:"tls"`
	Compression bool   `json:"compression"`
	SessionID   string `json:"session_id"`
}

// StationsResult lists the stations connected to a running `server start`.
type StationsResult struct {
	// Target is the control socket of the server asked.
	Target   string        `json:"target"`
	Stations []StationInfo `json:"stations"`
}

// MarshalTextOutput implements format.TextMarshaler.
func (r StationsResult) MarshalTextOutput() (string, error) {
	if len(r.Stations) == 0 {
		return "no station connected", nil
	}
	var b strings.Builder
	for _, s := range r.Stations {
		_, _ = fmt.Fprintf(&b, "%-20s OCPP %-6s %-22s connected %s  tls=%t compression=%t\n", s.Identity, s.Version, s.Remote,
			time.Time(s.ConnectedAt).UTC().Format(TimeLayout), s.TLS, s.Compression)
	}
	return b.String(), nil
}

// TableHeaders implements format.TableMarshaler.
func (r StationsResult) TableHeaders() []string {
	return []string{"IDENTITY", "VERSION", "REMOTE", "CONNECTED_AT", "TLS", "COMPRESSION"}
}

// TableRows implements format.TableMarshaler.
func (r StationsResult) TableRows() [][]string {
	rows := make([][]string, 0, len(r.Stations))
	for _, s := range r.Stations {
		rows = append(rows, []string{s.Identity, s.Version, s.Remote, time.Time(s.ConnectedAt).UTC().Format(TimeLayout),
			strconv.FormatBool(s.TLS), strconv.FormatBool(s.Compression)})
	}
	return rows
}
