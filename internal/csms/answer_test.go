// SPDX-License-Identifier: MIT

package csms

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/types"
)

// An action the version does not have is answered NotImplemented, and a SEND of one is
// reported with that code: go-ocpp's validator reports it as a *ocpp.CallError, not a
// validation error.
func TestUnknownActionIsNotImplemented(t *testing.T) {
	a := newAnswerer(Default(), func(types.ServerEvent) {})
	_, err := a.answer(ocpp.V16, "CS1", "NoSuchAction", json.RawMessage(`{}`))
	var ce *ocpp.CallError
	if !errors.As(err, &ce) || ce.Code != ocpp.NotImplemented {
		t.Fatalf("answer: %v, want a NotImplemented CALLERROR", err)
	}
	if got := errorCode(err); got != string(ocpp.NotImplemented) {
		t.Errorf("errorCode = %s, want NotImplemented", got)
	}
	if got := errorCode(errors.New("x")); got != string(ocpp.FormationViolation) {
		t.Errorf("errorCode of another error = %s, want FormationViolation", got)
	}
}
