// SPDX-License-Identifier: MIT

// Package clientops runs the one-shot commands of a Charging Station: connect, do one
// thing, disconnect.
package clientops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/answer"
	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/payload"
	"github.com/otfabric/ocppctl/internal/types"
)

// Common holds what every one-shot client command has besides the target.
type Common struct {
	conn.Target
	Boot         bool   `flag:"boot" env:"OCPPCTL_BOOT" desc:"Send a BootNotification first and require it to be Accepted (many CSMSs refuse anything before)"`
	BootVendor   string `flag:"boot-vendor" env:"OCPPCTL_BOOT_VENDOR" desc:"Vendor name of the --boot BootNotification"`
	BootModel    string `flag:"boot-model" env:"OCPPCTL_BOOT_MODEL" desc:"Model of the --boot BootNotification"`
	OutputFormat string `flag:"format" env:"OCPPCTL_OUTPUT_FORMAT" desc:"Output format: text, json or table"`
	Debug        bool   `flag:"-"`
}

// NewCommon returns the defaults.
func NewCommon() Common {
	return Common{Target: conn.NewTarget(), BootVendor: "OTFabric", BootModel: "ocppctl", OutputFormat: "text"}
}

// CallConfig configures `client call`.
type CallConfig struct {
	Common
	Payload string `flag:"payload" desc:"Request payload: JSON, @file, or - for stdin (default {})"`
	Raw     bool   `flag:"raw" desc:"Send the payload without validating it against the schema (for testing a CSMS); the response is still validated"`
	Action  string `flag:"-"`
}

// session is an open connection with the answers to the CSMS's CALLs.
type session struct {
	c       *conn.Conn
	v       ocpp.Version
	answers map[ocpp.Version]*answer.Default
}

func (s *session) received() []types.Received {
	out := []types.Received{}
	if a := s.answers[s.v]; a != nil {
		for _, r := range a.Received() {
			out = append(out, types.Received{Time: types.Millis(r.Time), Action: r.Action, Request: r.Request, Response: r.Response, ErrorCode: r.ErrorCode})
		}
	}
	return out
}

// open validates the target, connects, and sends the BootNotification of --boot.
func open(ctx context.Context, cfg *Common, stderr io.Writer) (*session, error) {
	p, err := cfg.Parse()
	if err != nil {
		return nil, err
	}
	s := &session{answers: map[ocpp.Version]*answer.Default{}}
	for _, v := range p.Versions {
		s.answers[v] = &answer.Default{Version: v}
	}
	c, err := conn.Dial(ctx, p, conn.Options{
		Handler: func(v ocpp.Version) ocpp.Dispatcher { return s.answers[v] },
		Debug:   cfg.Debug, Stderr: stderr,
	})
	if err != nil {
		return nil, err
	}
	s.c, s.v = c, c.Version()
	_, _ = fmt.Fprintf(stderr, "connected to %s as %s (OCPP %s)\n", p.URL, p.Identity, s.v)
	if cfg.Boot {
		req := payload.Boot{Vendor: cfg.BootVendor, Model: cfg.BootModel, Reason: "PowerUp"}.Build(s.v)
		var resp struct {
			Status string `json:"status"`
		}
		if err := s.call(ctx, "BootNotification", req, &resp); err != nil {
			s.close()
			return nil, err
		}
		if resp.Status != "Accepted" {
			s.close()
			return nil, errs.New(errs.KindNegative, errs.CodeBootNotAccepted,
				fmt.Sprintf("BootNotification was answered %q, not Accepted: the CSMS does not accept this station yet", resp.Status), nil).
				WithOCPP(map[string]string{"status": resp.Status})
		}
		_, _ = fmt.Fprintf(stderr, "BootNotification accepted\n")
	}
	return s, nil
}

func (s *session) close() { _ = s.c.Stop() }

// call sends a CALL and validates the response; resp may be nil.
func (s *session) call(ctx context.Context, action string, req json.RawMessage, resp any) error {
	var raw json.RawMessage
	if err := s.c.Call(ctx, action, req, &raw); err != nil {
		return ocpperr.Wrap(err, action, ocppx.StateChanging(action))
	}
	if err := ocppx.ValidateResponse(s.v, action, raw); err != nil {
		return ocpperr.Wrap(fmt.Errorf("%w: %w", ocpp.ErrProtocol, err), action, false)
	}
	if resp != nil {
		if p, ok := resp.(*json.RawMessage); ok {
			*p = raw
			return nil
		}
		return json.Unmarshal(raw, resp)
	}
	return nil
}

// checkAction checks that action is a CALL a station sends in v, unless raw.
func checkAction(v ocpp.Version, action string, raw bool) (ocppx.Action, error) {
	a, err := ocppx.Lookup(v, action)
	if err != nil {
		if raw {
			return ocppx.Action{Name: action, From: ocppx.FromStation, Kind: "call"}, nil
		}
		return a, errs.InvalidInput(errs.CodeUnknownAction, err.Error(), err)
	}
	if a.From == ocppx.FromCSMS && !raw {
		return a, errs.InvalidInput(errs.CodeUnknownAction,
			fmt.Sprintf("%s is sent by the CSMS, not by a station (--raw sends it anyway)", action), nil)
	}
	return a, nil
}

// Call runs `client call`.
func Call(ctx context.Context, cfg *CallConfig, stdin io.Reader, stderr io.Writer) (*types.CallResult, error) {
	req, err := conn.ReadPayload(cfg.Payload, stdin)
	if err != nil {
		return nil, err
	}
	// With one version offered, everything is checked before connecting.
	if p, err := cfg.Parse(); err != nil {
		return nil, err
	} else if len(p.Versions) == 1 {
		if err := checkRequest(p.Versions[0], cfg.Action, req, cfg.Raw); err != nil {
			return nil, err
		}
	}
	s, err := open(ctx, &cfg.Common, stderr)
	if err != nil {
		return nil, err
	}
	defer s.close()
	return callOn(ctx, s, cfg.Action, req, cfg.Raw)
}

func checkRequest(v ocpp.Version, action string, req json.RawMessage, raw bool) error {
	a, err := checkAction(v, action, raw)
	if err != nil {
		return err
	}
	if a.Kind == "send" {
		return errs.InvalidInput(errs.CodeUnknownAction, fmt.Sprintf("%s is a SEND: use \"ocppctl client send\"", action), nil)
	}
	if raw {
		return nil
	}
	if err := ocppx.ValidateRequest(v, action, req); err != nil {
		return ocpperr.Wrap(err, action, false)
	}
	return nil
}

// callOn sends one CALL on an open session and builds the result.
func callOn(ctx context.Context, s *session, action string, req json.RawMessage, raw bool) (*types.CallResult, error) {
	if err := checkRequest(s.v, action, req, raw); err != nil {
		return nil, err
	}
	start := time.Now()
	var resp json.RawMessage
	if err := s.call(ctx, action, req, &resp); err != nil {
		return nil, err
	}
	return &types.CallResult{
		Target: s.c.Parsed.URL, Version: s.v.String(), Action: action, Request: req, Response: resp,
		DurationMS: time.Since(start).Milliseconds(), Validated: !raw, Received: s.received(),
	}, nil
}

// SendConfig configures `client send`.
type SendConfig struct {
	Common
	Payload string `flag:"payload" desc:"Request payload: JSON, @file, or - for stdin"`
	Raw     bool   `flag:"raw" desc:"Send the payload without validating it against the schema"`
	Action  string `flag:"-"`
}

// Send runs `client send`: one SEND (OCPP 2.1), never answered.
func Send(ctx context.Context, cfg *SendConfig, stdin io.Reader, stderr io.Writer) (*types.SendResult, error) {
	req, err := conn.ReadPayload(cfg.Payload, stdin)
	if err != nil {
		return nil, err
	}
	if _, err := cfg.Parse(); err != nil {
		return nil, err
	}
	s, err := open(ctx, &cfg.Common, stderr)
	if err != nil {
		return nil, err
	}
	sent := false
	defer func() {
		if !sent {
			s.close()
		}
	}()
	if s.v != ocpp.V21 {
		return nil, errs.InvalidInput(errs.CodeUnsupportedVersion,
			fmt.Sprintf("SEND exists in OCPP 2.1 only; the CSMS chose %s (offer 2.1 with --ocpp-version 2.1)", s.v), nil)
	}
	a, err := checkAction(s.v, cfg.Action, cfg.Raw)
	if err != nil {
		return nil, err
	}
	if a.Kind != "send" && !cfg.Raw {
		return nil, errs.InvalidInput(errs.CodeUnknownAction, fmt.Sprintf("%s is a CALL: use \"ocppctl client call\"", cfg.Action), nil)
	}
	if !cfg.Raw {
		if err := ocppx.ValidateRequest(s.v, cfg.Action, req); err != nil {
			return nil, ocpperr.Wrap(err, cfg.Action, false)
		}
	}
	if err := s.c.Send(ctx, cfg.Action, req); err != nil {
		return nil, ocpperr.Wrap(err, cfg.Action, true)
	}
	// The graceful close returns once the SEND is written.
	sent = true
	if err := s.c.Stop(); err != nil {
		return nil, errs.New(errs.KindTimeout, errs.CodeOutcomeUnknown,
			fmt.Sprintf("the %s SEND was queued but not written within %s", cfg.Action, conn.ShutdownTimeout), err)
	}
	return &types.SendResult{Target: s.c.Parsed.URL, Version: s.v.String(), Action: cfg.Action, Request: req, Sent: true}, nil
}
