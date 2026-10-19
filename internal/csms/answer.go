// SPDX-License-Identifier: MIT

package csms

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// fallbacks are tried in order for an action the model has no answer for: the first that
// passes the action's response schema is sent (as internal/answer does).
var fallbacks = []json.RawMessage{
	json.RawMessage(`{}`),
	json.RawMessage(`{"status":"Accepted"}`),
}

// answerer answers the stations' CALLs from the model. It is shared by every version.
type answerer struct {
	model *Model
	emit  func(types.ServerEvent)
	now   func() time.Time

	mu     sync.Mutex
	nextTx int
}

func newAnswerer(m *Model, emit func(types.ServerEvent)) *answerer {
	return &answerer{model: m, emit: emit, now: time.Now, nextTx: *m.Transactions.FirstID}
}

// dispatcher is the ocpp.Dispatcher of one version.
type dispatcher struct {
	a *answerer
	v ocpp.Version
}

// Dispatch implements ocpp.Dispatcher.
func (d dispatcher) Dispatch(_ context.Context, p ocpp.Peer, action string, payload json.RawMessage) (json.RawMessage, error) {
	ev := types.NewServerEvent("call_received")
	ev.Identity, ev.Version, ev.Action, ev.Request = p.ID(), d.v.String(), action, payload
	d.a.emit(ev)

	resp, err := d.a.answer(d.v, p.ID(), action, payload)

	ev = types.NewServerEvent("answered")
	ev.Identity, ev.Version, ev.Action = p.ID(), d.v.String(), action
	var ce *ocpp.CallError
	switch {
	case errors.As(err, &ce):
		ev.ErrorCode, ev.ErrorDescription = string(ce.Code), ce.Description
	case err != nil:
		ev.ErrorCode = string(ocpp.InternalError)
	default:
		ev.Response = resp
	}
	d.a.emit(ev)
	return resp, err
}

// DispatchSend implements ocpp.SendDispatcher (OCPP 2.1): a SEND is recorded, never
// answered. An invalid one is reported with the error code it would have had.
func (d dispatcher) DispatchSend(_ context.Context, p ocpp.Peer, action string, payload json.RawMessage) error {
	ev := types.NewServerEvent("send_received")
	ev.Identity, ev.Version, ev.Action, ev.Request = p.ID(), d.v.String(), action, payload
	if err := ocppx.ValidateRequest(d.v, action, payload); err != nil {
		ev.ErrorCode, ev.ErrorDescription = errorCode(err), err.Error()
	}
	d.a.emit(ev)
	return nil
}

// errorCode is the CALLERROR code of a validation failure.
func errorCode(err error) string {
	var ve *ocpp.ValidationError
	if errors.As(err, &ve) {
		return string(ve.Code)
	}
	var ce *ocpp.CallError
	if errors.As(err, &ce) {
		return string(ce.Code)
	}
	return string(ocpp.FormationViolation)
}

// answer returns the CALLRESULT payload of a CALL, or a *ocpp.CallError.
func (a *answerer) answer(v ocpp.Version, id, action string, payload json.RawMessage) (json.RawMessage, error) {
	if err := ocppx.ValidateRequest(v, action, payload); err != nil {
		var ve *ocpp.ValidationError
		if errors.As(err, &ve) {
			return nil, ve.CallError()
		}
		var ce *ocpp.CallError
		if errors.As(err, &ce) {
			return nil, ce
		}
		return nil, ocpp.NewCallError(ocpp.FormationViolation, err.Error())
	}
	if o := a.model.override(v, id, action); o != nil {
		if o.Answer == AnswerNotSupported {
			return nil, ocpp.NewCallError(ocpp.NotSupported, "the CSMS model does not support "+action)
		}
		return o.payload, nil
	}
	if resp := a.builtin(v, id, action, payload); resp != nil && ocppx.ValidateResponse(v, action, resp) == nil {
		return resp, nil
	}
	for _, c := range fallbacks {
		if ocppx.ValidateResponse(v, action, c) == nil {
			return c, nil
		}
	}
	return nil, ocpp.NewCallError(ocpp.NotSupported, "the CSMS model has no answer for "+action)
}

// builtin is the model's answer to the actions it knows, or nil.
func (a *answerer) builtin(v ocpp.Version, id, action string, payload json.RawMessage) json.RawMessage {
	now := a.now().UTC().Format(types.TimeLayout)
	var req map[string]any
	_ = json.Unmarshal(payload, &req)
	switch action {
	case "BootNotification":
		return mustJSON(map[string]any{"status": a.model.bootStatus(id), "currentTime": now, "interval": *a.model.Boot.Interval})
	case "Heartbeat":
		return mustJSON(map[string]any{"currentTime": now})
	case "Authorize":
		if v == ocpp.V16 {
			return mustJSON(map[string]any{"idTagInfo": a.tokenInfo(str(req["idTag"]))})
		}
		return mustJSON(map[string]any{"idTokenInfo": a.tokenInfo(idToken(req["idToken"]))})
	case "StartTransaction":
		a.mu.Lock()
		tx := a.nextTx
		a.nextTx++
		a.mu.Unlock()
		return mustJSON(map[string]any{"transactionId": tx, "idTagInfo": a.tokenInfo(str(req["idTag"]))})
	case "StopTransaction":
		if tag := str(req["idTag"]); tag != "" {
			return mustJSON(map[string]any{"idTagInfo": a.tokenInfo(tag)})
		}
		return json.RawMessage(`{}`)
	case "TransactionEvent":
		if tok := idToken(req["idToken"]); tok != "" {
			return mustJSON(map[string]any{"idTokenInfo": a.tokenInfo(tok)})
		}
		return json.RawMessage(`{}`)
	case "DataTransfer":
		// 1.6 §4.3 and 2.x P01: a recipient without an implementation for the vendorId
		// answers UnknownVendorId. ocppctl implements no vendor extension.
		return json.RawMessage(`{"status":"UnknownVendorId"}`)
	}
	return nil
}

// tokenInfo is the idTagInfo (1.6) or idTokenInfo (2.x) of a token: both carry a status.
func (a *answerer) tokenInfo(token string) map[string]any {
	if a.model.tokenAccepted(token) {
		return map[string]any{"status": "Accepted"}
	}
	return map[string]any{"status": "Invalid"}
}

func idToken(v any) string {
	if m, ok := v.(map[string]any); ok {
		return str(m["idToken"])
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
