// SPDX-License-Identifier: MIT

// Package answer answers the CALLs a peer sends while ocppctl is busy with something else:
// with the smallest schema-valid response it can find, and records them.
package answer

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// Received is one CALL that arrived and how it was answered.
type Received struct {
	Time      time.Time       `json:"time"`
	Action    string          `json:"action"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

// candidates are tried in order: the first that passes the action's response schema is
// sent. Most CSMS-initiated CALLs are answered by a status.
var candidates = []json.RawMessage{
	json.RawMessage(`{}`),
	json.RawMessage(`{"status":"Accepted"}`),
}

// Default answers every CALL with the first candidate its response schema accepts, or the
// CALLERROR NotSupported, and records it. SENDs are recorded.
type Default struct {
	Version ocpp.Version
	mu      sync.Mutex
	got     []Received
}

// Received returns what arrived so far.
func (d *Default) Received() []Received {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Received{}, d.got...)
}

// Dispatch implements ocpp.Dispatcher.
func (d *Default) Dispatch(_ context.Context, _ ocpp.Peer, action string, payload json.RawMessage) (json.RawMessage, error) {
	r := Received{Time: time.Now().UTC(), Action: action, Request: payload}
	defer func() {
		d.mu.Lock()
		d.got = append(d.got, r)
		d.mu.Unlock()
	}()
	if err := ocppx.ValidateRequest(d.Version, action, payload); err != nil {
		var ve *ocpp.ValidationError
		if errors.As(err, &ve) {
			r.ErrorCode = string(ve.Code)
			return nil, ve.CallError()
		}
		var ce *ocpp.CallError
		if errors.As(err, &ce) {
			r.ErrorCode = string(ce.Code)
		}
		return nil, err
	}
	for _, c := range candidates {
		if ocppx.ValidateResponse(d.Version, action, c) == nil {
			r.Response = c
			return c, nil
		}
	}
	r.ErrorCode = string(ocpp.NotSupported)
	return nil, ocpp.NewCallError(ocpp.NotSupported, "ocppctl answers this action only in client run with a model")
}

// DispatchSend implements ocpp.SendDispatcher.
func (d *Default) DispatchSend(_ context.Context, _ ocpp.Peer, action string, payload json.RawMessage) error {
	d.mu.Lock()
	d.got = append(d.got, Received{Time: time.Now().UTC(), Action: action, Request: payload})
	d.mu.Unlock()
	return nil
}
