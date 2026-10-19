// SPDX-License-Identifier: MIT

package csms

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// The control socket protocol: one JSON object per line each way, a response for every
// request, any number of requests per connection (docs/server-config.md).

// Control operations.
const (
	OpStations = "stations"
	OpCall     = "call"
)

// maxControlLine bounds one request line.
const maxControlLine = 4 << 20

// ControlRequest is one request on the control socket.
type ControlRequest struct {
	Op string `json:"op"`
	// Station, Action, Payload, Raw and TimeoutMS are the CALL of a "call".
	Station   string          `json:"station,omitempty"`
	Action    string          `json:"action,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Raw       bool            `json:"raw,omitempty"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}

// ControlResponse is the response to one request.
type ControlResponse struct {
	OK       bool                `json:"ok"`
	Stations []types.StationInfo `json:"stations,omitempty"`
	Result   *types.CallResult   `json:"result,omitempty"`
	Error    *types.ErrorInfo    `json:"error,omitempty"`
	OCPP     json.RawMessage     `json:"ocpp,omitempty"`
}

// control is the listening control socket of a CSMS.
type control struct {
	ln   net.Listener
	path string
	c    *CSMS
	wg   sync.WaitGroup
	mu   sync.Mutex
	open map[net.Conn]bool
}

// listenControl creates the control socket at path, mode 0600. A socket left behind by a
// server that is gone is replaced; one a running server answers on, or a file that is not
// a socket, is not.
func listenControl(path string, c *CSMS) (*control, error) {
	fail := func(msg string, err error) error {
		return errs.New(errs.KindTransport, errs.CodeControlSocketFailed, msg, err)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fail(fmt.Sprintf("control socket %s: the file exists and is not a socket: choose another --control-socket", path), nil)
		}
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			_ = conn.Close()
			return nil, fail(fmt.Sprintf("control socket %s: another ocppctl server is running there: choose another --control-socket", path), nil)
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fail(fmt.Sprintf("control socket %s: %v (choose another with --control-socket, or disable it with --control-socket '')", path, err), err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fail(fmt.Sprintf("control socket %s: %v", path, err), err)
	}
	return &control{ln: ln, path: path, c: c, open: map[net.Conn]bool{}}, nil
}

func (ct *control) serve(ctx context.Context) {
	for {
		conn, err := ct.ln.Accept()
		if err != nil {
			return
		}
		ct.mu.Lock()
		ct.open[conn] = true
		ct.mu.Unlock()
		ct.wg.Add(1)
		go func() {
			defer ct.wg.Done()
			ct.handle(ctx, conn)
			ct.mu.Lock()
			delete(ct.open, conn)
			ct.mu.Unlock()
		}()
	}
}

// close stops accepting, ends the open connections and removes the socket.
func (ct *control) close() {
	_ = ct.ln.Close()
	ct.mu.Lock()
	for conn := range ct.open {
		_ = conn.Close()
	}
	ct.mu.Unlock()
	ct.wg.Wait()
	_ = os.Remove(ct.path)
}

func (ct *control) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), maxControlLine)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req ControlRequest
		var resp ControlResponse
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp = errorResponse(errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("control request is not JSON: %v", err), err))
		} else {
			resp = ct.c.Control(ctx, req)
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func errorResponse(err error) ControlResponse {
	e, ok := errs.As(err)
	if !ok {
		e = errs.Internal(err)
	}
	resp := ControlResponse{Error: &types.ErrorInfo{Kind: string(e.Kind), Code: e.Code, Message: e.Error()}}
	if e.OCPP != nil {
		resp.OCPP, _ = json.Marshal(e.OCPP)
	}
	return resp
}

// Control carries out one control request.
func (c *CSMS) Control(ctx context.Context, req ControlRequest) ControlResponse {
	switch req.Op {
	case OpStations:
		return ControlResponse{OK: true, Stations: c.Stations()}
	case OpCall:
		res, err := c.Call(ctx, req)
		if err != nil {
			return errorResponse(err)
		}
		return ControlResponse{OK: true, Result: res}
	}
	return errorResponse(errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("unknown control operation %q (expected %s or %s)", req.Op, OpStations, OpCall), nil))
}

// Call sends a CALL to a connected station and returns its validated response.
func (c *CSMS) Call(ctx context.Context, req ControlRequest) (*types.CallResult, error) {
	sess, ok := c.srv.Station(req.Station)
	if !ok {
		return nil, errs.New(errs.KindTransport, errs.CodeStationNotConnected,
			fmt.Sprintf("station %q is not connected (ocppctl server stations lists the stations that are)", req.Station), nil)
	}
	v := sess.Version()
	payload := req.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := checkCSMSCall(v, req.Action, payload, req.Raw); err != nil {
		return nil, err
	}
	timeout := c.callTimeout
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ev := types.NewServerEvent("call_sent")
	ev.Identity, ev.Version, ev.Action, ev.Request = sess.ID(), v.String(), req.Action, payload
	c.emit(ev)

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var resp json.RawMessage
	err := sess.Call(cctx, req.Action, payload, &resp)
	ms := time.Since(start).Milliseconds()
	ev = types.NewServerEvent("call_result")
	ev.Identity, ev.Version, ev.Action, ev.DurationMS = sess.ID(), v.String(), req.Action, &ms
	if err == nil {
		if verr := ocppx.ValidateResponse(v, req.Action, resp); verr != nil {
			err = fmt.Errorf("%w: %w", ocpp.ErrProtocol, verr)
		}
	}
	if err != nil {
		var ce *ocpp.CallError
		if errors.As(err, &ce) {
			ev.ErrorCode, ev.ErrorDescription = string(ce.Code), ce.Description
		} else {
			ev.Reason = err.Error()
		}
		if len(resp) > 0 {
			ev.Response = resp
		}
		c.emit(ev)
		return nil, ocpperr.Wrap(err, req.Action, ocppx.StateChanging(req.Action))
	}
	ev.Response = resp
	c.emit(ev)
	return &types.CallResult{Target: sess.ID(), Version: v.String(), Action: req.Action, Request: payload, Response: resp,
		DurationMS: ms, Validated: !req.Raw, Received: []types.Received{}}, nil
}

// checkCSMSCall checks that action is a CALL the CSMS sends in v and, unless raw, that the
// payload is valid.
func checkCSMSCall(v ocpp.Version, action string, payload json.RawMessage, raw bool) error {
	var probe map[string]any
	if err := json.Unmarshal(payload, &probe); err != nil {
		return errs.InvalidInput(errs.CodeInvalidPayload, "the payload must be a JSON object", err)
	}
	a, err := ocppx.Lookup(v, action)
	switch {
	case err != nil && raw && action != "":
		return nil
	case err != nil:
		return errs.InvalidInput(errs.CodeUnknownAction, err.Error(), err)
	case a.Kind == "send":
		return errs.InvalidInput(errs.CodeUnknownAction, fmt.Sprintf("%s is a SEND, not a CALL", action), nil)
	case raw:
		return nil
	case a.From == ocppx.FromStation:
		return errs.InvalidInput(errs.CodeUnknownAction,
			fmt.Sprintf("%s is sent by a station, not by the CSMS (--raw sends it anyway)", action), nil)
	}
	if err := ocppx.ValidateRequest(v, action, payload); err != nil {
		return ocpperr.Wrap(err, action, false)
	}
	return nil
}
