// SPDX-License-Identifier: MIT

// Package payload builds the requests of the client shortcuts for each OCPP version, from
// a few values. The result is validated against the schema before it is sent, like any
// other request.
package payload

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/otfabric/go-ocpp"
)

func marshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// Boot is a BootNotification.
type Boot struct{ Vendor, Model, Serial, Firmware, Reason string }

// Build returns the request for v.
func (b Boot) Build(v ocpp.Version) json.RawMessage {
	if v == ocpp.V16 {
		m := map[string]any{"chargePointVendor": b.Vendor, "chargePointModel": b.Model}
		if b.Serial != "" {
			m["chargePointSerialNumber"] = b.Serial
		}
		if b.Firmware != "" {
			m["firmwareVersion"] = b.Firmware
		}
		return marshal(m)
	}
	cs := map[string]any{"vendorName": b.Vendor, "model": b.Model}
	if b.Serial != "" {
		cs["serialNumber"] = b.Serial
	}
	if b.Firmware != "" {
		cs["firmwareVersion"] = b.Firmware
	}
	return marshal(map[string]any{"reason": b.Reason, "chargingStation": cs})
}

// Authorize is an Authorize of an id token.
type Authorize struct{ Token, Type string }

// Build returns the request for v.
func (a Authorize) Build(v ocpp.Version) json.RawMessage {
	if v == ocpp.V16 {
		return marshal(map[string]any{"idTag": a.Token})
	}
	return marshal(map[string]any{"idToken": idToken(a.Token, a.Type)})
}

func idToken(token, typ string) map[string]any { return map[string]any{"idToken": token, "type": typ} }

// Status is a StatusNotification. 1.6 reports a connector; 2.x an EVSE's connector.
type Status struct {
	EVSE, Connector int
	Status          string
	ErrorCode       string // 1.6 only
}

// Build returns the request for v.
func (s Status) Build(v ocpp.Version) json.RawMessage {
	if v == ocpp.V16 {
		return marshal(map[string]any{"connectorId": s.Connector, "errorCode": s.ErrorCode, "status": s.Status, "timestamp": now()})
	}
	return marshal(map[string]any{"timestamp": now(), "connectorStatus": s.Status, "evseId": s.EVSE, "connectorId": s.Connector})
}

// Meter is a MeterValues with the energy register and, optionally, the power.
type Meter struct {
	EVSE, Connector int
	EnergyWh        float64
	PowerW          float64 // 0: not sent
	Transaction     string  // 1.6: an integer transaction id
}

func sampled(v ocpp.Version, energy, power float64) []map[string]any {
	val := func(x float64) any {
		if v == ocpp.V16 {
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return x
	}
	out := []map[string]any{{"value": val(energy), "measurand": "Energy.Active.Import.Register"}}
	if v == ocpp.V16 {
		out[0]["unit"] = "Wh"
	} else {
		out[0]["unitOfMeasure"] = map[string]any{"unit": "Wh"}
	}
	if power > 0 {
		p := map[string]any{"value": val(power), "measurand": "Power.Active.Import"}
		if v == ocpp.V16 {
			p["unit"] = "W"
		} else {
			p["unitOfMeasure"] = map[string]any{"unit": "W"}
		}
		out = append(out, p)
	}
	return out
}

// Build returns the request for v.
func (m Meter) Build(v ocpp.Version) (json.RawMessage, error) {
	mv := []map[string]any{{"timestamp": now(), "sampledValue": sampled(v, m.EnergyWh, m.PowerW)}}
	if v == ocpp.V16 {
		req := map[string]any{"connectorId": m.Connector, "meterValue": mv}
		if m.Transaction != "" {
			id, err := strconv.Atoi(m.Transaction)
			if err != nil {
				return nil, fmt.Errorf("OCPP 1.6 transaction ids are integers: %q", m.Transaction)
			}
			req["transactionId"] = id
		}
		return marshal(req), nil
	}
	return marshal(map[string]any{"evseId": m.EVSE, "meterValue": mv}), nil
}

// DataTransfer is a vendor-specific DataTransfer.
type DataTransfer struct{ Vendor, MessageID, Data string }

// Build returns the request for v. In 2.x data is sent as JSON when it parses as JSON, as
// a string otherwise; 1.6 data is always a string.
func (d DataTransfer) Build(v ocpp.Version) json.RawMessage {
	m := map[string]any{"vendorId": d.Vendor}
	if d.MessageID != "" {
		m["messageId"] = d.MessageID
	}
	if d.Data != "" {
		var j any
		if v != ocpp.V16 && json.Unmarshal([]byte(d.Data), &j) == nil {
			m["data"] = j
		} else {
			m["data"] = d.Data
		}
	}
	return marshal(m)
}

// TransactionStart starts a charging session: StartTransaction in 1.6, a TransactionEvent
// Started in 2.x.
type TransactionStart struct {
	EVSE, Connector  int
	Token, TokenType string
	MeterStartWh     float64
	Transaction      string // 2.x: the station's transaction id
	TriggerReason    string // 2.x: default Authorized
	RemoteStartID    int    // 2.x: the id of a RequestStartTransaction; 0: none
}

// Build returns the action and request for v.
func (t TransactionStart) Build(v ocpp.Version) (string, json.RawMessage) {
	if v == ocpp.V16 {
		return "StartTransaction", marshal(map[string]any{"connectorId": t.Connector, "idTag": t.Token,
			"meterStart": int(t.MeterStartWh), "timestamp": now()})
	}
	trigger := t.TriggerReason
	if trigger == "" {
		trigger = "Authorized"
	}
	info := map[string]any{"transactionId": t.Transaction, "chargingState": "Charging"}
	if t.RemoteStartID != 0 {
		info["remoteStartId"] = t.RemoteStartID
	}
	return "TransactionEvent", marshal(map[string]any{
		"eventType": "Started", "timestamp": now(), "triggerReason": trigger, "seqNo": 0,
		"transactionInfo": info,
		"evse":            map[string]any{"id": t.EVSE, "connectorId": t.Connector},
		"idToken":         idToken(t.Token, t.TokenType),
		"meterValue":      []map[string]any{{"timestamp": now(), "sampledValue": sampled(v, t.MeterStartWh, 0)}},
	})
}

// TransactionStop ends a charging session: StopTransaction in 1.6, a TransactionEvent Ended
// in 2.x.
type TransactionStop struct {
	EVSE, Connector int
	Transaction     string
	MeterStopWh     float64
	Reason          string
	SeqNo           int
	TriggerReason   string // 2.x: default StopAuthorized
}

// Build returns the action and request for v.
func (t TransactionStop) Build(v ocpp.Version) (string, json.RawMessage, error) {
	if v == ocpp.V16 {
		id, err := strconv.Atoi(t.Transaction)
		if err != nil {
			return "", nil, fmt.Errorf("OCPP 1.6 transaction ids are integers: %q", t.Transaction)
		}
		req := map[string]any{"transactionId": id, "meterStop": int(t.MeterStopWh), "timestamp": now()}
		if t.Reason != "" {
			req["reason"] = t.Reason
		}
		return "StopTransaction", marshal(req), nil
	}
	info := map[string]any{"transactionId": t.Transaction, "chargingState": "Idle"}
	if t.Reason != "" {
		info["stoppedReason"] = t.Reason
	}
	trigger := t.TriggerReason
	if trigger == "" {
		trigger = "StopAuthorized"
	}
	return "TransactionEvent", marshal(map[string]any{
		"eventType": "Ended", "timestamp": now(), "triggerReason": trigger, "seqNo": t.SeqNo,
		"transactionInfo": info,
		"evse":            map[string]any{"id": t.EVSE, "connectorId": t.Connector},
		"meterValue":      []map[string]any{{"timestamp": now(), "sampledValue": sampled(v, t.MeterStopWh, 0)}},
	}), nil
}

// TransactionUpdate is a periodic meter sample of a running transaction: MeterValues with
// the transaction id in 1.6, a TransactionEvent Updated in 2.x.
type TransactionUpdate struct {
	EVSE, Connector  int
	Transaction      string
	EnergyWh, PowerW float64
	SeqNo            int
}

// Build returns the action and request for v.
func (t TransactionUpdate) Build(v ocpp.Version) (string, json.RawMessage, error) {
	if v == ocpp.V16 {
		req, err := Meter{Connector: t.Connector, EnergyWh: t.EnergyWh, PowerW: t.PowerW, Transaction: t.Transaction}.Build(v)
		return "MeterValues", req, err
	}
	return "TransactionEvent", marshal(map[string]any{
		"eventType": "Updated", "timestamp": now(), "triggerReason": "MeterValuePeriodic", "seqNo": t.SeqNo,
		"transactionInfo": map[string]any{"transactionId": t.Transaction, "chargingState": "Charging"},
		"evse":            map[string]any{"id": t.EVSE, "connectorId": t.Connector},
		"meterValue":      []map[string]any{{"timestamp": now(), "sampledValue": sampled(v, t.EnergyWh, t.PowerW)}},
	}), nil
}
