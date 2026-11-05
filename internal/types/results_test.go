// SPDX-License-Identifier: MIT

package types

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The response of a CALL is shown with its numbers as the peer sent them: a transaction
// id above 2^53 must not be rounded.
func TestCallResultTextKeepsNumbers(t *testing.T) {
	r := CallResult{Action: "StartTransaction", Version: "1.6", Response: json.RawMessage(`{"transactionId":9007199254740993,"idTagInfo":{"status":"Accepted"}}`)}
	text, _ := r.MarshalTextOutput()
	if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "\n  \"transactionId\"") {
		t.Errorf("response altered: %s", text)
	}
	p := PayloadResult{Payload: json.RawMessage(`{"meterStart":12345678901234567890}`)}
	if text, _ := p.MarshalTextOutput(); !strings.Contains(text, "12345678901234567890") {
		t.Errorf("payload altered: %s", text)
	}
	if got := indentJSON(json.RawMessage("not json")); got != "not json\n" {
		t.Errorf("not JSON: %q", got)
	}
}

// What a peer sent (its action names, its JSON with any whitespace in it) stays on the
// line of its event.
func TestPeerTextIsOneLine(t *testing.T) {
	e := StationEvent{Time: Millis(time.Unix(0, 0)), Station: "CS1", Event: "call_received", Action: "Reset\nforged",
		Request: json.RawMessage("{\n\"type\":\n\"Soft\"}")}
	text, _ := e.MarshalTextOutput()
	if strings.Contains(text, "\n") || !strings.Contains(text, `{"type":"Soft"}`) {
		t.Errorf("station event: %q", text)
	}
	for _, cell := range e.TableRows()[0] {
		if strings.Contains(cell, "\n") {
			t.Errorf("station event cell: %q", cell)
		}
	}
	r := CallResult{Action: "Reset", Response: json.RawMessage(`{}`), Received: []Received{{Action: "Heartbeat\nforged", Response: json.RawMessage(`{}`)}}}
	text, _ = r.MarshalTextOutput()
	if strings.Contains(text, "Heartbeat\nforged") {
		t.Errorf("received action: %q", text)
	}
	s := ScriptResult{Unexpected: []Received{{Action: "Heartbeat\nforged", ErrorCode: "NotSupported"}}}
	text, _ = s.MarshalTextOutput()
	if strings.Contains(text, "Heartbeat\nforged") {
		t.Errorf("unexpected action: %q", text)
	}
}

// Free text a peer chose (a CALLERROR description, a refusal, a certificate name, a step
// message quoting the CSMS) stays on its line in text output.
func TestPeerFreeTextIsOneLine(t *testing.T) {
	const forged = "x\nPASS     9  forged"
	outputs := map[string]interface{ MarshalTextOutput() (string, error) }{
		"probe refusal":     ProbeResult{Attempts: []ProbeAttempt{{Error: &ErrorInfo{Message: forged}}}},
		"probe tls":         ProbeResult{Attempts: []ProbeAttempt{{OK: true, TLS: &TLSInfo{Subject: forged, Issuer: forged}}}},
		"frame description": FrameResult{Type: "CALLERROR", ErrorCode: "GenericError", Description: forged},
		"station event":     StationEvent{Time: Millis(time.Unix(0, 0)), Station: "CS1", Event: "error", Message: forged},
		"step message":      ScriptResult{Steps: []StepResult{{Index: 1, Kind: "call", Result: "fail", Message: forged}}},
	}
	for name, o := range outputs {
		text, err := o.MarshalTextOutput()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(text, "\nPASS") {
			t.Errorf("%s: a forged line: %q", name, text)
		}
	}
}
