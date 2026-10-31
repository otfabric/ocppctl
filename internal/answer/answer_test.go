// SPDX-License-Identifier: MIT

package answer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/otfabric/go-ocpp"
)

func TestDispatchAnswersWithAValidResponse(t *testing.T) {
	d := &Default{Version: ocpp.V16}
	// Reset is answered {"status":"Accepted"}: {} fails its response schema.
	got, err := d.Dispatch(context.Background(), nil, "Reset", json.RawMessage(`{"type":"Soft"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"status":"Accepted"}` {
		t.Fatalf("response %s", got)
	}
	// GetDiagnostics has no required response field: the first candidate fits.
	got, err = d.Dispatch(context.Background(), nil, "GetDiagnostics", json.RawMessage(`{"location":"ftp://x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{}` {
		t.Fatalf("response %s", got)
	}
	rec := d.Received()
	if len(rec) != 2 || rec[0].Action != "Reset" || string(rec[0].Response) != `{"status":"Accepted"}` || string(rec[1].Response) != `{}` {
		t.Fatalf("recorded %+v", rec)
	}
}

func TestDispatchInvalidRequestIsRecordedWithItsCode(t *testing.T) {
	d := &Default{Version: ocpp.V16}
	_, err := d.Dispatch(context.Background(), nil, "Reset", json.RawMessage(`{"type":"Sideways"}`))
	var ce *ocpp.CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v, want a CALLERROR", err)
	}
	rec := d.Received()
	if len(rec) != 1 || rec[0].ErrorCode != string(ce.Code) || rec[0].Response != nil {
		t.Fatalf("recorded %+v", rec)
	}
}

func TestDispatchUnknownActionIsACallError(t *testing.T) {
	d := &Default{Version: ocpp.V16}
	_, err := d.Dispatch(context.Background(), nil, "NoSuchAction", json.RawMessage(`{}`))
	var ce *ocpp.CallError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v, want a CALLERROR", err)
	}
	if rec := d.Received(); len(rec) != 1 || rec[0].ErrorCode == "" {
		t.Fatalf("recorded %+v", rec)
	}
}

func TestDispatchNoCandidateFitsIsNotSupported(t *testing.T) {
	d := &Default{Version: ocpp.V16}
	// The response needs a listVersion: neither candidate has one.
	_, err := d.Dispatch(context.Background(), nil, "GetLocalListVersion", json.RawMessage(`{}`))
	var ce *ocpp.CallError
	if !errors.As(err, &ce) || ce.Code != ocpp.NotSupported {
		t.Fatalf("error %v, want CALLERROR NotSupported", err)
	}
	if rec := d.Received(); len(rec) != 1 || rec[0].ErrorCode != string(ocpp.NotSupported) {
		t.Fatalf("recorded %+v", rec)
	}
}

func TestDispatchSendIsRecorded(t *testing.T) {
	d := &Default{Version: ocpp.V201}
	if err := d.DispatchSend(context.Background(), nil, "NotifyPeriodicEventStream", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	rec := d.Received()
	if len(rec) != 1 || rec[0].Action != "NotifyPeriodicEventStream" || string(rec[0].Request) != `{"a":1}` || rec[0].Time.IsZero() {
		t.Fatalf("recorded %+v", rec)
	}
	rec[0].Action = "changed"
	if d.Received()[0].Action == "changed" {
		t.Error("Received hands out the internal slice")
	}
}
