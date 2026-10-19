// SPDX-License-Identifier: MIT

package clientops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/payload"
	"github.com/otfabric/ocppctl/internal/types"
)

// Builder builds the request of a shortcut for the negotiated version.
type Builder func(v ocpp.Version) (action string, req json.RawMessage, err error)

// Shortcut runs a shortcut command: it builds the request for the version the CSMS
// chooses and sends it like `client call`. With payloadOnly it prints the request for the
// one version offered and connects to nothing.
func Shortcut(ctx context.Context, cfg *Common, payloadOnly bool, build Builder, stderr io.Writer) (any, error) {
	if payloadOnly {
		return buildOnly(cfg, build)
	}
	if _, err := cfg.Parse(); err != nil {
		return nil, err
	}
	s, err := open(ctx, cfg, stderr)
	if err != nil {
		return nil, err
	}
	defer s.close()
	action, req, err := build(s.v)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	return callOn(ctx, s, action, req, false)
}

func buildOnly(cfg *Common, build Builder) (any, error) {
	vs, err := ocppx.ParseVersions(cfg.Versions)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	if len(vs) != 1 {
		return nil, errs.InvalidInput(errs.CodeInvalidFlagCombination,
			"--payload-only builds the request for one version: give exactly one --ocpp-version", nil)
	}
	action, req, err := build(vs[0])
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	if err := ocppx.ValidateRequest(vs[0], action, req); err != nil {
		return nil, ocpperr.Wrap(err, action, false)
	}
	return &types.PayloadResult{Version: vs[0].String(), Action: action, Payload: req}, nil
}

// Fixed returns a Builder that always builds action from f.
func Fixed(action string, f func(v ocpp.Version) json.RawMessage) Builder {
	return func(v ocpp.Version) (string, json.RawMessage, error) { return action, f(v), nil }
}

// BootBuilder builds a BootNotification.
func BootBuilder(b payload.Boot) Builder {
	return Fixed("BootNotification", b.Build)
}

// HeartbeatBuilder builds a Heartbeat.
func HeartbeatBuilder() Builder {
	return Fixed("Heartbeat", func(ocpp.Version) json.RawMessage { return json.RawMessage(`{}`) })
}

// AuthorizeBuilder builds an Authorize.
func AuthorizeBuilder(a payload.Authorize) Builder { return Fixed("Authorize", a.Build) }

// StatusBuilder builds a StatusNotification.
func StatusBuilder(s payload.Status) Builder { return Fixed("StatusNotification", s.Build) }

// MeterBuilder builds a MeterValues.
func MeterBuilder(m payload.Meter) Builder {
	return func(v ocpp.Version) (string, json.RawMessage, error) {
		req, err := m.Build(v)
		return "MeterValues", req, err
	}
}

// DataTransferBuilder builds a DataTransfer.
func DataTransferBuilder(d payload.DataTransfer) Builder { return Fixed("DataTransfer", d.Build) }

// StartBuilder builds the start of a transaction. In 2.x the station chooses the
// transaction id; an empty one is generated.
func StartBuilder(t payload.TransactionStart) Builder {
	return func(v ocpp.Version) (string, json.RawMessage, error) {
		if v != ocpp.V16 && t.Transaction == "" {
			t.Transaction = NewTransactionID()
		}
		action, req := t.Build(v)
		return action, req, nil
	}
}

// StopBuilder builds the end of a transaction.
func StopBuilder(t payload.TransactionStop) Builder {
	return func(v ocpp.Version) (string, json.RawMessage, error) { return t.Build(v) }
}

// NewTransactionID returns a random transaction id for OCPP 2.x (at most 36 characters).
func NewTransactionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}
