// SPDX-License-Identifier: MIT

package ocpperr

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"

	"github.com/otfabric/ocppctl/internal/errs"
)

func TestMeaningCoversTheRefusals(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnauthorized:       "authentication",
		http.StatusForbidden:          "forbidden",
		http.StatusNotFound:           "identity",
		http.StatusConflict:           "already connected",
		http.StatusBadRequest:         "identifierString",
		http.StatusServiceUnavailable: "try later",
		http.StatusTeapot:             "teapot",
	} {
		if got := Meaning(status); !strings.Contains(got, want) {
			t.Errorf("%d: %q does not say %q", status, got, want)
		}
	}
}

func TestWrapClassifies(t *testing.T) {
	for name, c := range map[string]struct {
		err   error
		state bool
		act   string
		kind  errs.Kind
		code  string
	}{
		"nil-like canceled":  {err: context.Canceled, kind: errs.KindInterrupted, code: errs.CodeContextCanceled},
		"callerror":          {err: ocpp.NewCallError(ocpp.NotSupported, "no"), act: "Reset", kind: errs.KindNegative, code: errs.CodeCallError},
		"handshake":          {err: &client.HandshakeError{StatusCode: 401}, kind: errs.KindTransport, code: errs.CodeHandshakeRefused},
		"no common version":  {err: fmt.Errorf("%w: %w", ocpp.ErrConnectFailed, ocpp.ErrUnsupportedVersion), kind: errs.KindTransport, code: errs.CodeNoCommonVersion},
		"tls":                {err: tls.RecordHeaderError{Msg: "x"}, kind: errs.KindTransport, code: errs.CodeTransportTLSFailed},
		"connect":            {err: ocpp.ErrConnectFailed, kind: errs.KindTransport, code: errs.CodeTransportConnectFailed},
		"timeout, state":     {err: ocpp.ErrTimeout, act: "Reset", state: true, kind: errs.KindTimeout, code: errs.CodeOutcomeUnknown},
		"timeout, read":      {err: context.DeadlineExceeded, act: "GetBaseReport", kind: errs.KindTimeout, code: errs.CodeRequestTimeout},
		"timeout, no action": {err: ocpp.ErrTimeout, state: true, kind: errs.KindTimeout, code: errs.CodeRequestTimeout},
		"lost":               {err: ocpp.ErrConnectionLost, kind: errs.KindTransport, code: errs.CodeConnectionLost},
		"not connected":      {err: ocpp.ErrNotConnected, kind: errs.KindTransport, code: errs.CodeConnectionLost},
		"closed":             {err: ocpp.ErrClosed, kind: errs.KindTransport, code: errs.CodeConnectionLost},
		"version":            {err: ocpp.ErrUnsupportedVersion, kind: errs.KindInvalidInput, code: errs.CodeUnsupportedVersion},
		"direction":          {err: ocpp.ErrWrongDirection, kind: errs.KindInvalidInput, code: errs.CodeUnknownAction},
		"option":             {err: ocpp.ErrInvalidOption, kind: errs.KindInvalidInput, code: errs.CodeInvalidConfig},
		"protocol":           {err: ocpp.ErrProtocol, kind: errs.KindProtocol, code: errs.CodeProtocolViolation},
	} {
		t.Run(name, func(t *testing.T) {
			e, ok := errs.As(Wrap(c.err, c.act, c.state))
			if !ok {
				t.Fatalf("not typed: %v", c.err)
			}
			if e.Kind != c.kind || e.Code != c.code {
				t.Fatalf("%s/%s, want %s/%s", e.Kind, e.Code, c.kind, c.code)
			}
		})
	}
}

func TestWrapCallErrorCarriesTheDetail(t *testing.T) {
	ce := ocpp.NewCallError(ocpp.NotSupported, "nope")
	err := Wrap(ce, "Reset", false)
	e, _ := errs.As(err)
	if !strings.Contains(e.Message, "NotSupported: nope") {
		t.Errorf("message %q", e.Message)
	}
	if d, ok := e.OCPP.(CallErrorDetail); !ok || d.ErrorCode != "NotSupported" || d.Description != "nope" {
		t.Errorf("detail %+v", e.OCPP)
	}
}

func TestWrapHandshakeCarriesTheStatus(t *testing.T) {
	e, _ := errs.As(Wrap(&client.HandshakeError{StatusCode: http.StatusConflict}, "", false))
	if d, ok := e.OCPP.(HandshakeDetail); !ok || d.HTTPStatus != 409 || !strings.Contains(d.Meaning, "already") {
		t.Errorf("detail %+v", e.OCPP)
	}
}

func TestWrapLeavesTypedAndNilAlone(t *testing.T) {
	if Wrap(nil, "", false) != nil {
		t.Error("nil became an error")
	}
	typed := errs.Internal(errors.New("x"))
	if got := Wrap(typed, "", false); !errors.Is(got, typed) {
		t.Errorf("typed error replaced by %v", got)
	}
	if e, ok := errs.As(Wrap(errors.New("what"), "", false)); !ok || e.Kind != errs.KindInternal {
		t.Errorf("unknown error: %+v", e)
	}
}
