// SPDX-License-Identifier: MIT

package station

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// dispatcher answers the CSMS's CALLs on one version for a station.
type dispatcher struct {
	s *Station
	v ocpp.Version
}

func marshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

var (
	accepted = json.RawMessage(`{"status":"Accepted"}`)
	rejected = json.RawMessage(`{"status":"Rejected"}`)
	empty    = json.RawMessage(`{}`)
)

// Dispatch implements ocpp.Dispatcher: the model's answer, else the built-in behaviour,
// else the first schema-valid default. Every response is validated before it is sent.
func (d *dispatcher) Dispatch(ctx context.Context, _ ocpp.Peer, action string, req json.RawMessage) (json.RawMessage, error) {
	s := d.s
	s.emit(types.StationEvent{Event: "call_received", Version: d.v.String(), Action: action, Request: req})
	resp, err := d.answer(ctx, action, req)
	if err == nil {
		if verr := ocppx.ValidateResponse(d.v, action, resp); verr != nil {
			s.emit(types.StationEvent{Event: "error", Action: action, Message: "the answer is not valid for OCPP " + d.v.String() + ": " + verr.Error()})
			err = ocpp.NewCallError(ocpp.InternalError, "the simulator's answer is not valid for this version")
			resp = nil
		}
	}
	ev := types.StationEvent{Event: "answered", Version: d.v.String(), Action: action, Response: resp}
	var ce *ocpp.CallError
	if errors.As(err, &ce) {
		ev.ErrorCode = string(ce.Code)
	} else if err != nil {
		ev.ErrorCode = string(ocpp.InternalError)
	}
	s.emit(ev)
	return resp, err
}

// DispatchSend implements ocpp.SendDispatcher (OCPP 2.1).
func (d *dispatcher) DispatchSend(_ context.Context, _ ocpp.Peer, action string, req json.RawMessage) error {
	d.s.emit(types.StationEvent{Event: "send_received", Version: d.v.String(), Action: action, Request: req})
	return nil
}

func (d *dispatcher) answer(ctx context.Context, action string, req json.RawMessage) (json.RawMessage, error) {
	if err := ocppx.ValidateRequest(d.v, action, req); err != nil {
		var ve *ocpp.ValidationError
		if errors.As(err, &ve) {
			return nil, ve.CallError()
		}
		return nil, err
	}
	if a, ok := d.s.model.Answers[action]; ok {
		if resp, err, ok := d.fromModel(action, a); ok {
			return resp, err
		}
	}
	if resp, err, ok := d.builtin(ctx, action, req); ok {
		return resp, err
	}
	for _, c := range []json.RawMessage{empty, accepted} {
		if ocppx.ValidateResponse(d.v, action, c) == nil {
			return c, nil
		}
	}
	return nil, ocpp.NewCallError(ocpp.NotSupported, "the simulated station has no answer for "+action)
}

// fromModel answers from the model's answers. A static answer that is not valid for this
// version (the model may serve several) is skipped.
func (d *dispatcher) fromModel(action string, a Answer) (json.RawMessage, error, bool) {
	switch {
	case a.Static != nil:
		if ocppx.ValidateResponse(d.v, action, a.Static) == nil {
			return a.Static, nil, true
		}
		return nil, nil, false
	case a.Behaviour == Accept:
		for _, c := range []json.RawMessage{accepted, empty} {
			if ocppx.ValidateResponse(d.v, action, c) == nil {
				return c, nil, true
			}
		}
	case a.Behaviour == Reject:
		if ocppx.ValidateResponse(d.v, action, rejected) == nil {
			return rejected, nil, true
		}
		return nil, ocpp.NewCallError(ocpp.NotSupported, action+" is refused by the simulated station"), true
	case a.Behaviour == NotSupported:
		return nil, ocpp.NewCallError(ocpp.NotSupported, action+" is not supported by the simulated station"), true
	case a.Behaviour == NotImplemented:
		return nil, ocpp.NewCallError(ocpp.NotImplemented, action+" is not implemented by the simulated station"), true
	}
	return nil, nil, false
}

// builtin is the behaviour of the actions the simulator really implements.
func (d *dispatcher) builtin(ctx context.Context, action string, req json.RawMessage) (json.RawMessage, error, bool) {
	s := d.s
	switch action {
	case "RemoteStartTransaction", "RequestStartTransaction":
		return d.remoteStart(ctx, req)
	case "RemoteStopTransaction", "RequestStopTransaction":
		var r struct {
			TransactionID json.RawMessage `json:"transactionId"`
		}
		_ = json.Unmarshal(req, &r)
		id := strings.Trim(string(r.TransactionID), `"`)
		if !s.hasTransaction(id) {
			return rejected, nil, true
		}
		ocpp.AfterResponse(ctx, func() { s.enqueue(func(ctx context.Context) { s.stopSession(ctx, id, "Remote", "RemoteStop") }) })
		return accepted, nil, true
	case "Reset":
		return d.reset(ctx, req)
	case "TriggerMessage":
		return d.trigger(ctx, req)
	case "GetConfiguration":
		return d.getConfiguration(req), nil, true
	case "ChangeConfiguration":
		return d.changeConfiguration(req), nil, true
	case "GetVariables":
		return d.getVariables(req), nil, true
	case "SetVariables":
		return d.setVariables(req), nil, true
	case "GetLocalListVersion":
		// The simulator keeps no local authorization list: 1.6 says so with -1, 2.x with 0.
		if d.v == ocpp.V16 {
			return json.RawMessage(`{"listVersion":-1}`), nil, true
		}
		return json.RawMessage(`{"versionNumber":0}`), nil, true
	case "GetTransactionStatus":
		var r struct {
			TransactionID string `json:"transactionId"`
		}
		_ = json.Unmarshal(req, &r)
		out := map[string]any{"messagesInQueue": false}
		if r.TransactionID != "" {
			out["ongoingIndicator"] = s.hasTransaction(r.TransactionID)
		}
		return marshal(out), nil, true
	case "ClearTariffs":
		// The simulator holds no tariffs.
		return json.RawMessage(`{"clearTariffsResult":[{"status":"NoTariff"}]}`), nil, true
	case "CustomerInformation":
		// It holds no customer data either, and sends no NotifyCustomerInformation.
		return rejected, nil, true
	case "UnpublishFirmware":
		return json.RawMessage(`{"status":"NoFirmware"}`), nil, true
	case "ClearVariableMonitoring":
		var r struct {
			ID []int `json:"id"`
		}
		_ = json.Unmarshal(req, &r)
		results := []map[string]any{}
		for _, id := range r.ID {
			results = append(results, map[string]any{"status": "NotFound", "id": id})
		}
		return marshal(map[string]any{"clearMonitoringResult": results}), nil, true
	case "SetVariableMonitoring":
		return d.setVariableMonitoring(req), nil, true
	case "GetBaseReport":
		var r struct {
			RequestID int `json:"requestId"`
		}
		_ = json.Unmarshal(req, &r)
		ocpp.AfterResponse(ctx, func() { s.enqueue(func(ctx context.Context) { s.notifyReport(ctx, r.RequestID) }) })
		return accepted, nil, true
	}
	return nil, nil, false
}

func (d *dispatcher) remoteStart(ctx context.Context, req json.RawMessage) (json.RawMessage, error, bool) {
	s := d.s
	var r struct {
		ConnectorID   int    `json:"connectorId"`
		IDTag         string `json:"idTag"`
		EVSEID        int    `json:"evseId"`
		RemoteStartID int    `json:"remoteStartId"`
		IDToken       struct {
			IDToken string `json:"idToken"`
			Type    string `json:"type"`
		} `json:"idToken"`
	}
	_ = json.Unmarshal(req, &r)
	sess := &session{remote: true, power: s.model.RemotePowerW, remoteStartID: r.RemoteStartID}
	if sess.power == 0 {
		sess.power = 11000
	}
	if d.v == ocpp.V16 {
		sess.token = r.IDTag
		if r.ConnectorID != 0 {
			e, c, ok := s.model.from16(r.ConnectorID)
			if !ok {
				return rejected, nil, true
			}
			sess.evse, sess.connector = e, c
		}
	} else {
		sess.token, sess.tokenType, sess.evse = r.IDToken.IDToken, r.IDToken.Type, r.EVSEID
	}
	if !s.claim(sess) {
		return rejected, nil, true
	}
	if d.v != ocpp.V16 {
		s.setTx(sess, newTransactionID())
	}
	ocpp.AfterResponse(ctx, func() { s.enqueue(func(ctx context.Context) { s.startSession(ctx, sess) }) })
	if d.v == ocpp.V16 {
		return accepted, nil, true
	}
	return marshal(map[string]any{"status": "Accepted", "transactionId": sess.tx}), nil, true
}

var triggerable = map[string]bool{
	"BootNotification": true, "Heartbeat": true, "StatusNotification": true, "MeterValues": true,
	"FirmwareStatusNotification": true, "DiagnosticsStatusNotification": true, "LogStatusNotification": true,
}

func (d *dispatcher) trigger(ctx context.Context, req json.RawMessage) (json.RawMessage, error, bool) {
	s := d.s
	var r struct {
		RequestedMessage string `json:"requestedMessage"`
	}
	_ = json.Unmarshal(req, &r)
	if !triggerable[r.RequestedMessage] {
		if ocppx.ValidateResponse(d.v, "TriggerMessage", json.RawMessage(`{"status":"NotImplemented"}`)) == nil {
			return json.RawMessage(`{"status":"NotImplemented"}`), nil, true
		}
		return rejected, nil, true
	}
	if !known(d.v, r.RequestedMessage) {
		return rejected, nil, true
	}
	ocpp.AfterResponse(ctx, func() { s.enqueue(func(ctx context.Context) { s.triggered(ctx, r.RequestedMessage) }) })
	return accepted, nil, true
}

func (d *dispatcher) getConfiguration(req json.RawMessage) json.RawMessage {
	s := d.s
	var r struct {
		Key []string `json:"key"`
	}
	_ = json.Unmarshal(req, &r)
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := r.Key
	if len(keys) == 0 {
		keys = sortedKeys(s.conf)
	}
	known := []map[string]any{}
	unknown := []string{}
	for _, k := range keys {
		c, ok := s.conf[k]
		if !ok {
			unknown = append(unknown, k)
			continue
		}
		known = append(known, map[string]any{"key": k, "readonly": c.Readonly, "value": c.Value})
	}
	out := map[string]any{"configurationKey": known}
	if len(unknown) > 0 {
		out["unknownKey"] = unknown
	}
	return marshal(out)
}

func (d *dispatcher) changeConfiguration(req json.RawMessage) json.RawMessage {
	s := d.s
	var r struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	_ = json.Unmarshal(req, &r)
	s.mu.Lock()
	c, ok := s.conf[r.Key]
	switch {
	case !ok:
		s.mu.Unlock()
		return json.RawMessage(`{"status":"NotSupported"}`)
	case c.Readonly:
		s.mu.Unlock()
		return rejected
	}
	c.Value = r.Value
	s.conf[r.Key] = c
	s.mu.Unlock()
	if r.Key == "HeartbeatInterval" {
		s.setHeartbeatSeconds(r.Value)
	}
	return accepted
}

type varRef struct {
	Component struct {
		Name string `json:"name"`
	} `json:"component"`
	Variable struct {
		Name string `json:"name"`
	} `json:"variable"`
}

func (s *Station) componentKnown(name string) bool {
	for k := range s.vars {
		if strings.HasPrefix(k, name+".") {
			return true
		}
	}
	return false
}

func (d *dispatcher) getVariables(req json.RawMessage) json.RawMessage {
	s := d.s
	var r struct {
		Data []struct {
			varRef
			AttributeType string `json:"attributeType"`
		} `json:"getVariableData"`
	}
	_ = json.Unmarshal(req, &r)
	var orig struct {
		Data []struct {
			Component json.RawMessage `json:"component"`
			Variable  json.RawMessage `json:"variable"`
		} `json:"getVariableData"`
	}
	_ = json.Unmarshal(req, &orig)
	s.mu.Lock()
	defer s.mu.Unlock()
	results := []map[string]any{}
	for i, g := range r.Data {
		res := map[string]any{"component": orig.Data[i].Component, "variable": orig.Data[i].Variable}
		if g.AttributeType != "" {
			res["attributeType"] = g.AttributeType
		}
		v, ok := s.vars[g.Component.Name+"."+g.Variable.Name]
		switch {
		case g.AttributeType != "" && g.AttributeType != "Actual":
			res["attributeStatus"] = "NotSupportedAttributeType"
		case !ok && !s.componentKnown(g.Component.Name):
			res["attributeStatus"] = "UnknownComponent"
		case !ok:
			res["attributeStatus"] = "UnknownVariable"
		case v.Mutability == "WriteOnly":
			res["attributeStatus"] = "Rejected"
		default:
			res["attributeStatus"], res["attributeValue"] = "Accepted", v.Value
		}
		results = append(results, res)
	}
	return marshal(map[string]any{"getVariableResult": results})
}

func (d *dispatcher) setVariables(req json.RawMessage) json.RawMessage {
	s := d.s
	var r struct {
		Data []struct {
			varRef
			AttributeType  string `json:"attributeType"`
			AttributeValue string `json:"attributeValue"`
		} `json:"setVariableData"`
	}
	_ = json.Unmarshal(req, &r)
	var orig struct {
		Data []struct {
			Component json.RawMessage `json:"component"`
			Variable  json.RawMessage `json:"variable"`
		} `json:"setVariableData"`
	}
	_ = json.Unmarshal(req, &orig)
	results := []map[string]any{}
	var heartbeat string
	s.mu.Lock()
	for i, g := range r.Data {
		res := map[string]any{"component": orig.Data[i].Component, "variable": orig.Data[i].Variable}
		if g.AttributeType != "" {
			res["attributeType"] = g.AttributeType
		}
		name := g.Component.Name + "." + g.Variable.Name
		v, ok := s.vars[name]
		switch {
		case g.AttributeType != "" && g.AttributeType != "Actual":
			res["attributeStatus"] = "NotSupportedAttributeType"
		case !ok && !s.componentKnown(g.Component.Name):
			res["attributeStatus"] = "UnknownComponent"
		case !ok:
			res["attributeStatus"] = "UnknownVariable"
		case v.Mutability == "ReadOnly":
			res["attributeStatus"] = "Rejected"
		default:
			v.Value = g.AttributeValue
			s.vars[name] = v
			res["attributeStatus"] = "Accepted"
			if name == "OCPPCommCtrlr.HeartbeatInterval" {
				heartbeat = v.Value
			}
		}
		results = append(results, res)
	}
	s.mu.Unlock()
	if heartbeat != "" {
		s.setHeartbeatSeconds(heartbeat)
	}
	return marshal(map[string]any{"setVariableResult": results})
}

func (s *Station) setHeartbeatSeconds(v string) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return
	}
	s.setHeartbeat(time.Duration(n) * time.Second)
}

// known reports whether action exists in v.
func known(v ocpp.Version, action string) bool {
	_, err := ocppx.Lookup(v, action)
	return err == nil
}

// setVariableMonitoring refuses every monitor: the simulator does no monitoring.
func (d *dispatcher) setVariableMonitoring(req json.RawMessage) json.RawMessage {
	s := d.s
	var r struct {
		Data []struct {
			varRef
			Type     string `json:"type"`
			Severity int    `json:"severity"`
		} `json:"setMonitoringData"`
	}
	_ = json.Unmarshal(req, &r)
	var orig struct {
		Data []struct {
			Component json.RawMessage `json:"component"`
			Variable  json.RawMessage `json:"variable"`
		} `json:"setMonitoringData"`
	}
	_ = json.Unmarshal(req, &orig)
	s.mu.Lock()
	defer s.mu.Unlock()
	results := []map[string]any{}
	for i, m := range r.Data {
		status := "Rejected"
		_, ok := s.vars[m.Component.Name+"."+m.Variable.Name]
		switch {
		case !ok && !s.componentKnown(m.Component.Name):
			status = "UnknownComponent"
		case !ok:
			status = "UnknownVariable"
		}
		results = append(results, map[string]any{"status": status, "type": m.Type, "severity": m.Severity,
			"component": orig.Data[i].Component, "variable": orig.Data[i].Variable})
	}
	return marshal(map[string]any{"setMonitoringResult": results})
}

// reset accepts a Reset of the whole station. 1.6 Hard and Soft reset at once, ending the
// sessions with reason HardReset or SoftReset; 2.x Immediate (and 2.1 ImmediateAndResume)
// at once with ImmediateReset, OnIdle at once when nothing charges and otherwise
// Scheduled, once the last session ends. A Reset of one EVSE is Rejected.
func (d *dispatcher) reset(ctx context.Context, req json.RawMessage) (json.RawMessage, error, bool) {
	s := d.s
	var r struct {
		Type   string `json:"type"`
		EVSEID *int   `json:"evseId"`
	}
	_ = json.Unmarshal(req, &r)
	if r.EVSEID != nil {
		return rejected, nil, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case d.v == ocpp.V16 && r.Type == "Soft":
		s.resetReason = "SoftReset"
	case d.v == ocpp.V16:
		s.resetReason = "HardReset"
	case r.Type == "OnIdle":
		s.resetReason = "ImmediateReset"
		if len(s.sessions) > 0 {
			s.resetOnIdle = true
			return json.RawMessage(`{"status":"Scheduled"}`), nil, true
		}
	default:
		s.resetReason = "ImmediateReset"
	}
	ocpp.AfterResponse(ctx, func() { s.requestReset() })
	return accepted, nil, true
}
