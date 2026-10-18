// SPDX-License-Identifier: MIT

package csms

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// ControlConfig is what the commands that talk to a running server share.
type ControlConfig struct {
	ControlSocket string `flag:"control-socket" env:"OCPPCTL_CONTROL_SOCKET" desc:"Control socket of the running ocppctl server start"`
	OutputFormat  string `flag:"format" env:"OCPPCTL_OUTPUT_FORMAT" desc:"Output format: text, json or table"`
}

// NewControlConfig returns the defaults.
func NewControlConfig() ControlConfig {
	return ControlConfig{ControlSocket: DefaultControlSocket, OutputFormat: "text"}
}

// CallConfig configures `server call`.
type CallConfig struct {
	ControlConfig
	Station string `flag:"station" env:"OCPPCTL_STATION" desc:"Identity of the connected station to send the CALL to"`
	Payload string `flag:"payload" desc:"Request payload: JSON, @file, or - for stdin (default {})"`
	Raw     bool   `flag:"raw" desc:"Send the payload without validating it against the schema (for testing a station); the response is still validated"`
	Yes     bool   `flag:"yes" desc:"Confirm a CALL that changes the station (Reset, RequestStartTransaction, ...): it may be a real charger"`
	Timeout string `flag:"timeout" env:"OCPPCTL_TIMEOUT" desc:"How long to wait for the station's response"`
	Action  string `flag:"-"`
}

// NewCallConfig returns the defaults.
func NewCallConfig() CallConfig {
	return CallConfig{ControlConfig: NewControlConfig(), Timeout: "30s"}
}

// Call runs `server call`.
func Call(ctx context.Context, cfg *CallConfig, stdin io.Reader) (*types.CallResult, error) {
	if strings.TrimSpace(cfg.Station) == "" {
		return nil, errs.New(errs.KindUsage, errs.CodeInvalidInput, "--station is required", nil)
	}
	if !cfg.Raw && !knownAction(cfg.Action) {
		_, err := ocppx.Lookup(ocpp.V21, cfg.Action)
		return nil, errs.InvalidInput(errs.CodeUnknownAction, err.Error(), err)
	}
	if ocppx.StateChanging(cfg.Action) && !cfg.Yes {
		return nil, errs.New(errs.KindUsage, errs.CodeConfirmationRequired,
			fmt.Sprintf("%s may change the state of station %s, which may be a real charger: add --yes to send it", cfg.Action, cfg.Station), nil)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout))
	if err != nil || timeout <= 0 {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --timeout %q: want a positive duration such as 30s", cfg.Timeout), err)
	}
	payload, err := conn.ReadPayload(cfg.Payload, stdin)
	if err != nil {
		return nil, err
	}
	resp, err := request(ctx, cfg.ControlSocket, ControlRequest{Op: OpCall, Station: cfg.Station, Action: cfg.Action,
		Payload: payload, Raw: cfg.Raw, TimeoutMS: timeout.Milliseconds()}, timeout+10*time.Second)
	if err != nil {
		return nil, err
	}
	if resp.Result == nil {
		return nil, errs.Internal(fmt.Errorf("the server sent no result"))
	}
	return resp.Result, nil
}

// knownAction reports whether action is an action of any OCPP version.
func knownAction(action string) bool {
	for _, v := range []ocpp.Version{ocpp.V21, ocpp.V201, ocpp.V16} {
		if _, err := ocppx.Lookup(v, action); err == nil {
			return true
		}
	}
	return false
}

// Stations runs `server stations`.
func Stations(ctx context.Context, cfg *ControlConfig) (*types.StationsResult, error) {
	resp, err := request(ctx, cfg.ControlSocket, ControlRequest{Op: OpStations}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	out := &types.StationsResult{Target: cfg.ControlSocket, Stations: resp.Stations}
	if out.Stations == nil {
		out.Stations = []types.StationInfo{}
	}
	return out, nil
}

// request sends one request to the control socket and returns its successful response; a
// failure the server reports is returned as the typed error it was.
func request(ctx context.Context, path string, req ControlRequest, wait time.Duration) (*ControlResponse, error) {
	if path == "" {
		return nil, errs.New(errs.KindUsage, errs.CodeInvalidInput, "--control-socket is empty", nil)
	}
	fail := func(err error) error {
		return errs.New(errs.KindTransport, errs.CodeControlSocketFailed,
			fmt.Sprintf("control socket %s: %v (is \"ocppctl server start\" running with this --control-socket? It defaults to %s in the current directory, or %s)",
				path, err, DefaultControlSocket, ControlSocketEnv), err)
	}
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := d.DialContext(dctx, "unix", path)
	if err != nil {
		return nil, fail(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(wait))
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	defer stop()
	line, err := json.Marshal(req)
	if err != nil {
		return nil, errs.Internal(err)
	}
	if _, err := c.Write(append(line, '\n')); err != nil {
		return nil, fail(err)
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), maxControlLine)
	if !sc.Scan() {
		if ctx.Err() != nil {
			return nil, errs.New(errs.KindInternal, errs.CodeContextCanceled, "interrupted", ctx.Err())
		}
		err := sc.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, fail(err)
	}
	var resp ControlResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return nil, fail(fmt.Errorf("unreadable response: %w", err))
	}
	if !resp.OK {
		if resp.Error == nil {
			return nil, errs.Internal(fmt.Errorf("the server reported a failure without an error"))
		}
		e := errs.New(errs.Kind(resp.Error.Kind), resp.Error.Code, resp.Error.Message, nil)
		if len(resp.OCPP) > 0 {
			e = e.WithOCPP(resp.OCPP)
		}
		return nil, e
	}
	return &resp, nil
}
