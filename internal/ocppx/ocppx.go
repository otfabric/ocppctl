// SPDX-License-Identifier: MIT

// Package ocppx is what ocppctl needs of each OCPP version, in one place: parsing version
// names, the actions of a version with their sender, and schema validation through the
// version packages of go-ocpp.
package ocppx

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpp16"
	"github.com/otfabric/go-ocpp/ocpp201"
	"github.com/otfabric/go-ocpp/ocpp21"
)

// DefaultVersions are offered when none is given: newest first, so that a CSMS that
// supports several picks the newest.
var DefaultVersions = []string{"2.1", "2.0.1", "1.6"}

// Sender of an action.
const (
	FromStation = "station"
	FromCSMS    = "csms"
	FromBoth    = "both"
)

// Action is one action of a version.
type Action struct {
	Name string `json:"name"`
	// From is "station", "csms" or "both" (DataTransfer).
	From string `json:"from"`
	// Kind is "call", or "send" for an OCPP 2.1 SEND, which is never answered.
	Kind string `json:"kind"`
}

// ParseVersion accepts "1.6", "2.0.1", "2.1", and the subprotocol names ("ocpp1.6", ...).
func ParseVersion(s string) (ocpp.Version, error) {
	s = strings.TrimSpace(s)
	if v, ok := ocpp.ParseVersion(s); ok {
		return v, nil
	}
	if v, ok := ocpp.ParseSubprotocol(s); ok {
		return v, nil
	}
	return 0, fmt.Errorf("unknown OCPP version %q (expected 1.6, 2.0.1 or 2.1)", s)
}

// ParseVersions parses a list of versions, in order, without repeats.
func ParseVersions(list []string) ([]ocpp.Version, error) {
	var out []ocpp.Version
	seen := map[ocpp.Version]bool{}
	for _, item := range list {
		for _, s := range strings.Split(item, ",") {
			if strings.TrimSpace(s) == "" {
				continue
			}
			v, err := ParseVersion(s)
			if err != nil {
				return nil, err
			}
			if seen[v] {
				return nil, fmt.Errorf("OCPP version %s given twice", v)
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no OCPP version given")
	}
	return out, nil
}

func from(r ocpp.Role) string {
	switch r {
	case ocpp.ChargingStation:
		return FromStation
	case ocpp.CSMS:
		return FromCSMS
	}
	return FromBoth
}

func kind(send bool) string {
	if send {
		return "send"
	}
	return "call"
}

// Actions returns the actions of v, sorted by name.
func Actions(v ocpp.Version) []Action {
	var out []Action
	switch v {
	case ocpp.V16:
		for _, a := range ocpp16.Actions {
			out = append(out, Action{a.Name, from(a.From), kind(a.Send)})
		}
	case ocpp.V201:
		for _, a := range ocpp201.Actions {
			out = append(out, Action{a.Name, from(a.From), kind(a.Send)})
		}
	case ocpp.V21:
		for _, a := range ocpp21.Actions {
			out = append(out, Action{a.Name, from(a.From), kind(a.Send)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds action in v, case-sensitively as OCPP names are (a case-insensitive match is
// suggested in the error).
func Lookup(v ocpp.Version, action string) (Action, error) {
	var near string
	for _, a := range Actions(v) {
		if a.Name == action {
			return a, nil
		}
		if strings.EqualFold(a.Name, action) {
			near = a.Name
		}
	}
	if near != "" {
		return Action{}, fmt.Errorf("%q is not an action of OCPP %s; did you mean %s?", action, v, near)
	}
	return Action{}, fmt.Errorf("%q is not an action of OCPP %s (ocppctl actions --ocpp-version %s lists them)", action, v, v)
}

// ValidateRequest validates a request payload of action in version v.
func ValidateRequest(v ocpp.Version, action string, payload json.RawMessage) error {
	switch v {
	case ocpp.V16:
		return ocpp16.ValidateRequest(action, payload)
	case ocpp.V201:
		return ocpp201.ValidateRequest(action, payload)
	case ocpp.V21:
		return ocpp21.ValidateRequest(action, payload)
	}
	return fmt.Errorf("unknown OCPP version %v", v)
}

// ValidateResponse validates a response payload of action in version v.
func ValidateResponse(v ocpp.Version, action string, payload json.RawMessage) error {
	switch v {
	case ocpp.V16:
		return ocpp16.ValidateResponse(action, payload)
	case ocpp.V201:
		return ocpp201.ValidateResponse(action, payload)
	case ocpp.V21:
		return ocpp21.ValidateResponse(action, payload)
	}
	return fmt.Errorf("unknown OCPP version %v", v)
}

// readOnly are the actions whose request changes nothing at the receiver: one that times
// out is "no answer"; any other is "outcome unknown", and never sent again.
var readOnly = map[string]bool{
	"Heartbeat": true, "Authorize": true, "GetCertificateStatus": true, "GetCertificateChainStatus": true,
	"Get15118EVCertificate": true, "GetConfiguration": true, "GetVariables": true, "GetBaseReport": true,
	"GetReport": true, "GetMonitoringReport": true, "GetLocalListVersion": true, "GetCompositeSchedule": true,
	"GetChargingProfiles": true, "GetDisplayMessages": true, "GetInstalledCertificateIds": true,
	"GetTransactionStatus": true, "GetTariffs": true, "GetDERControl": true, "GetPeriodicEventStream": true,
	"VatNumberValidation": true,
}

// StateChanging reports whether a request of action may change state at the receiver.
func StateChanging(action string) bool { return !readOnly[action] }
