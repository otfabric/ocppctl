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

// MaxKept and MaxKeptBytes bound what a command keeps of the CALLs and SENDs a peer sends
// while it does something else: the first MaxKept of them, as long as their payloads
// (request and response) total at most MaxKeptBytes. Every one after that is answered as
// before and counted, not kept.
const (
	MaxKept      = 1000
	MaxKeptBytes = 8 << 20
)

// Log keeps what a peer sent, within MaxKept and MaxKeptBytes.
type Log struct {
	mu      sync.Mutex
	kept    []Received
	bytes   int
	full    bool
	dropped int
}

// Add keeps r, or counts it once the log is full.
func (l *Log) Add(r Received) {
	size := len(r.Request) + len(r.Response)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full || len(l.kept) == MaxKept || l.bytes+size > MaxKeptBytes {
		l.full = true
		l.dropped++
		return
	}
	l.kept = append(l.kept, r)
	l.bytes += size
}

// Received returns what was kept so far.
func (l *Log) Received() []Received {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Received{}, l.kept...)
}

// Dropped returns how many were counted and not kept.
func (l *Log) Dropped() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}

// Default answers every CALL with the first candidate its response schema accepts, or the
// CALLERROR NotSupported, and records it. SENDs are recorded.
type Default struct {
	Version ocpp.Version
	log     Log
}

// Received returns what arrived so far, within MaxKept and MaxKeptBytes.
func (d *Default) Received() []Received { return d.log.Received() }

// Dropped returns how many CALLs and SENDs arrived past MaxKept or MaxKeptBytes.
func (d *Default) Dropped() int { return d.log.Dropped() }

// Dispatch implements ocpp.Dispatcher.
func (d *Default) Dispatch(_ context.Context, _ ocpp.Peer, action string, payload json.RawMessage) (json.RawMessage, error) {
	r := Received{Time: time.Now().UTC(), Action: action, Request: payload}
	resp, err := Answer(d.Version, action, payload)
	r.Response = resp
	if ce, ok := errors.AsType[*ocpp.CallError](err); ok {
		r.ErrorCode = string(ce.Code)
	}
	d.log.Add(r)
	return resp, err
}

// Answer answers a CALL as Default does, without recording it: with the first candidate
// the action's response schema accepts, or a CALLERROR.
func Answer(v ocpp.Version, action string, payload json.RawMessage) (json.RawMessage, error) {
	if err := ocppx.ValidateRequest(v, action, payload); err != nil {
		if ve, ok := errors.AsType[*ocpp.ValidationError](err); ok {
			return nil, ve.CallError()
		}
		return nil, err
	}
	for _, c := range candidates {
		if ocppx.ValidateResponse(v, action, c) == nil {
			return c, nil
		}
	}
	return nil, ocpp.NewCallError(ocpp.NotSupported, "ocppctl answers this action only in client run with a model")
}

// DispatchSend implements ocpp.SendDispatcher.
func (d *Default) DispatchSend(_ context.Context, _ ocpp.Peer, action string, payload json.RawMessage) error {
	d.log.Add(Received{Time: time.Now().UTC(), Action: action, Request: payload})
	return nil
}
