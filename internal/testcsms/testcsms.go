// SPDX-License-Identifier: MIT

// Package testcsms starts an in-process CSMS for ocppctl's tests: go-ocpp's ocpptest
// server with a recorder per version that answers what a station sends with schema-valid
// responses.
package testcsms

import (
	"testing"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocpptest"
	"github.com/otfabric/go-ocpp/server"
)

// Time is the currentTime of every answer.
const Time = "2026-01-01T00:00:00.000Z"

// CSMS is a running test CSMS.
type CSMS struct {
	Server *server.Server
	// URL ends with a slash: append the identity.
	URL       string
	Recorders map[ocpp.Version]*ocpptest.Recorder
}

// Recorder returns the recorder of v.
func (c *CSMS) Recorder(v ocpp.Version) *ocpptest.Recorder { return c.Recorders[v] }

// Answers returns a recorder with the standard answers of v.
func Answers(v ocpp.Version) *ocpptest.Recorder {
	r := ocpptest.NewRecorder().
		Answer("BootNotification", `{"status":"Accepted","currentTime":"`+Time+`","interval":300}`).
		Answer("Heartbeat", `{"currentTime":"`+Time+`"}`).
		Answer("StatusNotification", `{}`).
		Answer("MeterValues", `{}`).
		Answer("FirmwareStatusNotification", `{}`).
		Answer("DataTransfer", `{"status":"Accepted"}`)
	if v == ocpp.V16 {
		return r.Answer("Authorize", `{"idTagInfo":{"status":"Accepted"}}`).
			Answer("StartTransaction", `{"transactionId":7,"idTagInfo":{"status":"Accepted"}}`).
			Answer("StopTransaction", `{}`).
			Answer("DiagnosticsStatusNotification", `{}`)
	}
	return r.Answer("Authorize", `{"idTokenInfo":{"status":"Accepted"}}`).
		Answer("TransactionEvent", `{}`).
		Answer("NotifyReport", `{}`).
		Answer("LogStatusNotification", `{}`)
}

// Start starts a CSMS for every version, plus opts.
func Start(t testing.TB, opts ...server.Option) *CSMS {
	t.Helper()
	c := &CSMS{Recorders: map[ocpp.Version]*ocpptest.Recorder{}}
	all := []server.Option{}
	for _, v := range []ocpp.Version{ocpp.V16, ocpp.V201, ocpp.V21} {
		c.Recorders[v] = Answers(v)
		all = append(all, server.WithHandler(v, c.Recorders[v]))
	}
	c.Server, c.URL = ocpptest.CSMS(t, append(all, opts...)...)
	return c
}
