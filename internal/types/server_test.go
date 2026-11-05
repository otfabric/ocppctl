// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A station chooses its identity (the URL) and the action of its CALLs: neither may break
// the one-line-per-event text output nor forge another event.
func TestServerEventTextIsOneLine(t *testing.T) {
	forged := "\n2026-01-01T00:00:00.000Z  connected      ADMIN"
	e := NewServerEvent("call_received")
	e.Identity, e.Action, e.Version, e.Remote = "CS1"+forged, "Heartbeat"+forged+"\r\x1b[2J", "1.6", "127.0.0.1:1"
	e.Request = json.RawMessage(`{"a":"x\ny"}`)
	text, err := e.MarshalTextOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(text, "\n\r\x1b") {
		t.Errorf("text output is not one safe line: %q", text)
	}
	if !strings.Contains(text, `"CS1\n2026`) || !strings.Contains(text, `"Heartbeat\n2026`) {
		t.Errorf("the identity and action are not quoted: %s", text)
	}
	for _, row := range e.TableRows() {
		for _, cell := range row {
			if strings.ContainsAny(cell, "\n\r\x1b") {
				t.Errorf("table cell is not one safe line: %q", cell)
			}
		}
	}

	// Plain values stay as they are.
	e = NewServerEvent("connected")
	e.Identity, e.Action = "CS-001_a.b", "BootNotification"
	text, _ = e.MarshalTextOutput()
	if !strings.Contains(text, "  CS-001_a.b") || strings.Contains(text, `"`) {
		t.Errorf("plain identity: %s", text)
	}

	r := StationsResult{Stations: []StationInfo{{Identity: "CS1" + forged, Version: "1.6", ConnectedAt: Millis(time.Unix(0, 0))}}}
	text, _ = r.MarshalTextOutput()
	if strings.Count(text, "\n") != 1 {
		t.Errorf("stations text has a line per station only: %q", text)
	}
	for _, row := range r.TableRows() {
		if strings.ContainsAny(row[0], "\n\r") {
			t.Errorf("stations table cell: %q", row[0])
		}
	}
}

// The request and response are shown as they were sent: compacted, but with numbers and
// key order intact.
func TestServerEventTextKeepsPayload(t *testing.T) {
	e := NewServerEvent("call_received")
	e.Request = json.RawMessage("{\"transactionId\": 9007199254740993,\n \"b\": 1, \"a\": \"<&>\"}")
	e.Response = json.RawMessage(`not json`)
	text, _ := e.MarshalTextOutput()
	if !strings.Contains(text, `request={"transactionId":9007199254740993,"b":1,"a":"<&>"}`) {
		t.Errorf("request altered: %s", text)
	}
	if !strings.Contains(text, `response="not json"`) {
		t.Errorf("invalid JSON is not quoted: %s", text)
	}
}
