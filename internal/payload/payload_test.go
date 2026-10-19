// SPDX-License-Identifier: MIT

package payload

import (
	"testing"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// Every shortcut payload passes the schema of every version.
func TestPayloadsValidate(t *testing.T) {
	for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
		check := func(action string, b []byte) {
			t.Helper()
			if err := ocppx.ValidateRequest(v, action, b); err != nil {
				t.Errorf("OCPP %s %s %s: %v", v, action, b, err)
			}
		}
		check("BootNotification", Boot{Vendor: "OT Fabric", Model: "Sim", Serial: "S1", Firmware: "1.0", Reason: "PowerUp"}.Build(v))
		check("BootNotification", Boot{Vendor: "OT Fabric", Model: "Sim", Reason: "PowerUp"}.Build(v))
		check("Authorize", Authorize{Token: "04A1B2C3", Type: "ISO14443"}.Build(v))
		status := "Available"
		check("StatusNotification", Status{EVSE: 1, Connector: 1, Status: status, ErrorCode: "NoError"}.Build(v))
		m, err := Meter{EVSE: 1, Connector: 1, EnergyWh: 1234.5, PowerW: 11000, Transaction: "7"}.Build(v)
		if err != nil {
			t.Fatal(err)
		}
		check("MeterValues", m)
		check("DataTransfer", DataTransfer{Vendor: "com.example", MessageID: "x", Data: `{"a":1}`}.Build(v))
		check("DataTransfer", DataTransfer{Vendor: "com.example"}.Build(v))
		action, start := TransactionStart{EVSE: 1, Connector: 1, Token: "04A1B2C3", TokenType: "ISO14443", MeterStartWh: 100, Transaction: "TX-1"}.Build(v)
		check(action, start)
		reason := "Local"
		action, stop, err := TransactionStop{EVSE: 1, Connector: 1, Transaction: "7", MeterStopWh: 5000, Reason: reason, SeqNo: 1}.Build(v)
		if err != nil {
			t.Fatal(err)
		}
		check(action, stop)
	}
	if _, err := (Meter{Transaction: "TX"}).Build(ocpp.V16); err == nil {
		t.Error("a non-integer 1.6 transaction id was accepted")
	}
	if _, _, err := (TransactionStop{Transaction: "TX"}).Build(ocpp.V16); err == nil {
		t.Error("a non-integer 1.6 transaction id was accepted")
	}
}
