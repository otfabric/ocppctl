// SPDX-License-Identifier: MIT

package ocppx

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
)

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]ocpp.Version{"1.6": ocpp.V16, " 2.0.1 ": ocpp.V201, "ocpp2.1": ocpp.V21, "ocpp1.6": ocpp.V16} {
		if got, err := ParseVersion(in); err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "2.0", "OCPP1.6", "1.6J", "ocpp2.0"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q): accepted", in)
		}
	}
}

func TestParseVersions(t *testing.T) {
	got, err := ParseVersions([]string{"2.1, 1.6", "", " ,2.0.1"})
	if err != nil || len(got) != 3 || got[0] != ocpp.V21 || got[1] != ocpp.V16 || got[2] != ocpp.V201 {
		t.Fatalf("ParseVersions: %v, %v", got, err)
	}
	for name, in := range map[string][]string{
		"twice":   {"1.6", "ocpp1.6"},
		"none":    {" , ", ""},
		"nil":     nil,
		"unknown": {"1.6,3.0"},
	} {
		if _, err := ParseVersions(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestActionsAndLookup(t *testing.T) {
	for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
		as := Actions(v)
		if len(as) == 0 {
			t.Fatalf("%s: no actions", v)
		}
		for i := 1; i < len(as); i++ {
			if as[i-1].Name >= as[i].Name {
				t.Fatalf("%s: not sorted: %s, %s", v, as[i-1].Name, as[i].Name)
			}
		}
		if a, err := Lookup(v, "Heartbeat"); err != nil || a.From != FromStation || a.Kind != "call" {
			t.Errorf("%s Heartbeat: %+v, %v", v, a, err)
		}
		if a, err := Lookup(v, "DataTransfer"); err != nil || a.From != FromBoth {
			t.Errorf("%s DataTransfer: %+v, %v", v, a, err)
		}
		if _, err := Lookup(v, "heartbeat"); err == nil || !strings.Contains(err.Error(), "did you mean Heartbeat") {
			t.Errorf("%s: case-insensitive near miss: %v", v, err)
		}
		if _, err := Lookup(v, "Nope"); err == nil || !strings.Contains(err.Error(), "actions --ocpp-version") {
			t.Errorf("%s: unknown: %v", v, err)
		}
	}
	if a, err := Lookup(ocpp.V21, "NotifyPeriodicEventStream"); err != nil || a.Kind != "send" {
		t.Errorf("2.1 SEND: %+v, %v", a, err)
	}
	if Actions(ocpp.Version(0)) != nil {
		t.Error("an unknown version has actions")
	}
}

func TestValidate(t *testing.T) {
	for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
		if err := ValidateRequest(v, "Heartbeat", json.RawMessage(`{}`)); err != nil {
			t.Errorf("%s request: %v", v, err)
		}
		if err := ValidateResponse(v, "Heartbeat", json.RawMessage(`{"currentTime":"2026-01-01T00:00:00Z"}`)); err != nil {
			t.Errorf("%s response: %v", v, err)
		}
		if err := ValidateResponse(v, "Heartbeat", json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s: a response without currentTime passed", v)
		}
	}
	if ValidateRequest(ocpp.Version(0), "Heartbeat", nil) == nil || ValidateResponse(ocpp.Version(9), "Heartbeat", nil) == nil {
		t.Error("an unknown version validated")
	}
}

func TestStateChanging(t *testing.T) {
	for action, want := range map[string]bool{"Heartbeat": false, "GetVariables": false, "Authorize": false,
		"BootNotification": true, "RemoteStartTransaction": true, "GetLog": true, "": true} {
		if got := StateChanging(action); got != want {
			t.Errorf("StateChanging(%q) = %v", action, got)
		}
	}
}
