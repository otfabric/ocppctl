// SPDX-License-Identifier: MIT

package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/answer"
	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// Config configures `client script`.
type Config struct {
	conn.Target
	KeepGoing    bool     `flag:"keep-going" desc:"Run the remaining steps after a step fails (default: skip them)"`
	Vars         []string `flag:"var" desc:"Variable NAME=VALUE for ${NAME} in payloads (repeatable)"`
	OutputFormat string   `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
	Debug        bool     `flag:"-"`
	File         string   `flag:"-"`
}

// incoming is a CALL from the CSMS that a wait_for step is to answer.
type incoming struct {
	action string
	req    json.RawMessage
	reply  chan reply
	at     time.Time
}

type reply struct {
	resp json.RawMessage
	err  error
}

// runner runs one scenario.
type runner struct {
	cfg  *Config
	sc   *Scenario
	p    *conn.Parsed
	errw io.Writer

	c    *conn.Conn
	v    ocpp.Version
	vars map[string]any

	mu     sync.Mutex
	cursor int
	quiet  bool     // in expect_no_call
	noise  []string // CALLs that arrived during expect_no_call
	inbox  chan *incoming
	// ended is closed when the scenario has run: CALLs still held for a step
	// get the default answer, so that the connection can close gracefully.
	ended      chan struct{}
	pending    []*incoming
	unexpected []types.Received
}

// Prepare loads and checks the scenario and the target.
func Prepare(cfg *Config) (*Scenario, *conn.Parsed, error) {
	sc, err := Load(cfg.File)
	if err != nil {
		return nil, nil, errs.InvalidInput(errs.CodeInvalidConfig, err.Error(), err)
	}
	t := cfg.Target
	if sc.version != 0 {
		t.Versions = []string{sc.version.String()}
	}
	p, err := t.Parse()
	if err != nil {
		return nil, nil, err
	}
	for _, kv := range cfg.Vars {
		if name, _, ok := strings.Cut(kv, "="); !ok || !varRE.MatchString("${"+name+"}") {
			return nil, nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --var %q: want NAME=VALUE", kv), nil)
		}
	}
	return sc, p, nil
}

// Run connects and runs the steps. It fails only when the first connection does: a step
// that fails is a result.
func Run(ctx context.Context, cfg *Config, sc *Scenario, p *conn.Parsed, stderr io.Writer) (*types.ScriptResult, error) {
	r := &runner{cfg: cfg, sc: sc, p: p, errw: stderr, vars: map[string]any{}, inbox: make(chan *incoming, 64), ended: make(chan struct{})}
	for _, kv := range cfg.Vars {
		name, value, _ := strings.Cut(kv, "=")
		r.vars[name] = value
	}
	start := time.Now()
	if err := r.dial(ctx); err != nil {
		return nil, err
	}
	defer r.end() // on an early return; a completed run has ended already
	_, _ = fmt.Fprintf(stderr, "connected to %s as %s (OCPP %s)\n", p.URL, p.Identity, r.v)
	res := &types.ScriptResult{Scenario: sc.Name, Target: p.URL, Version: r.v.String(), Steps: []types.StepResult{}}
	failed := false
	for i := range sc.Steps {
		st := &sc.Steps[i]
		kind, action := st.Kind()
		sr := types.StepResult{Index: i + 1, Name: st.Name, Kind: kind, Action: action}
		if failed && !cfg.KeepGoing {
			sr.Result = "skipped"
			res.Skipped++
			res.Steps = append(res.Steps, sr)
			continue
		}
		r.mu.Lock()
		r.cursor = i
		r.mu.Unlock()
		t0 := time.Now()
		err := r.step(ctx, st, &sr)
		sr.DurationMS = time.Since(t0).Milliseconds()
		if ctx.Err() != nil {
			return nil, ocpperr.Wrap(ctx.Err(), "", false)
		}
		if err != nil {
			sr.Result, sr.Message = "fail", err.Error()
			res.Failed++
			failed = true
		} else {
			sr.Result = "pass"
			res.Passed++
		}
		res.Steps = append(res.Steps, sr)
		_, _ = fmt.Fprintf(stderr, "%s %d %s %s\n", sr.Result, sr.Index, kind, action)
	}
	r.end()
	r.mu.Lock()
	r.cursor = len(sc.Steps)
	res.Unexpected = append([]types.Received{}, r.unexpected...)
	r.mu.Unlock()
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// end answers the CALLs still held for a step that did not run and closes
// the connection gracefully. It may be called more than once.
func (r *runner) end() {
	select {
	case <-r.ended:
		return
	default:
		close(r.ended)
	}
	if r.c != nil {
		_ = r.c.Stop()
		r.c = nil
	}
}

func (r *runner) dial(ctx context.Context) error {
	c, err := conn.Dial(ctx, r.p, conn.Options{
		Handler: func(v ocpp.Version) ocpp.Dispatcher { return &dispatcher{r: r, v: v, def: &answer.Default{Version: v}} },
		Debug:   r.cfg.Debug, Stderr: r.errw,
	})
	if err != nil {
		return err
	}
	r.c, r.v = c, c.Version()
	return nil
}

func (r *runner) timeout(st *Step) time.Duration {
	if st.Timeout > 0 {
		return time.Duration(st.Timeout)
	}
	return r.p.Timeout
}

// step runs one step; an error fails it.
func (r *runner) step(ctx context.Context, st *Step, sr *types.StepResult) error {
	kind, action := st.Kind()
	switch kind {
	case "call", "send":
		if r.c == nil {
			return errors.New("not connected: a disconnect step came before, without reconnect")
		}
		req, err := r.payload(st.Payload.JSON)
		if err != nil {
			return err
		}
		sr.Request = req
		if !st.Raw {
			if err := ocppx.ValidateRequest(r.v, action, req); err != nil {
				return fmt.Errorf("the payload is not valid for OCPP %s: %w", r.v, err)
			}
		}
		if kind == "send" {
			if err := r.c.Send(ctx, action, req); err != nil {
				return err
			}
			return nil
		}
		return r.call(ctx, st, action, req, sr)
	case "wait_for":
		return r.waitFor(ctx, st, action, sr)
	case "expect_no_call":
		return r.expectNoCall(ctx, time.Duration(st.ExpectNoCall))
	case "sleep":
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(st.Sleep)):
		}
		return nil
	case "disconnect":
		if r.c == nil {
			return errors.New("already disconnected")
		}
		err := r.c.Stop()
		r.c = nil
		return err
	case "reconnect":
		if r.c != nil {
			_ = r.c.Stop()
		}
		if err := r.dial(ctx); err != nil {
			r.c = nil
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown step")
}

func (r *runner) payload(raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		raw = json.RawMessage(`{}`)
	}
	return substitute(raw, r.vars, r.p.Identity)
}

func (r *runner) call(ctx context.Context, st *Step, action string, req json.RawMessage, sr *types.StepResult) error {
	cctx, cancel := context.WithTimeout(ctx, r.timeout(st))
	defer cancel()
	var resp json.RawMessage
	err := r.c.Call(cctx, action, req, &resp)
	var ce *ocpp.CallError
	if errors.As(err, &ce) {
		sr.ErrorCode = string(ce.Code)
		if st.ExpectError != "" {
			if st.ExpectError == "any" || st.ExpectError == string(ce.Code) {
				return nil
			}
			return fmt.Errorf("answered with CALLERROR %s, want %s", ce.Code, st.ExpectError)
		}
		return fmt.Errorf("answered with CALLERROR %s: %s", ce.Code, ce.Description)
	}
	if err != nil {
		if errors.Is(err, ocpp.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("no response within %s", r.timeout(st))
		}
		return err
	}
	sr.Response = resp
	if verr := ocppx.ValidateResponse(r.v, action, resp); verr != nil {
		return fmt.Errorf("the response is not valid for OCPP %s: %w", r.v, verr)
	}
	if st.ExpectError != "" {
		return fmt.Errorf("answered with a CALLRESULT, want CALLERROR %s", st.ExpectError)
	}
	if fails := check(resp, st.Expect); len(fails) > 0 {
		return errors.New(strings.Join(fails, "; "))
	}
	return r.save(resp, st.Save)
}

func (r *runner) save(doc json.RawMessage, save map[string]string) error {
	var v any
	_ = json.Unmarshal(doc, &v)
	for name, path := range save {
		x, ok := lookup(v, path)
		if !ok {
			return fmt.Errorf("save %s: %s is absent", name, path)
		}
		r.vars[name] = x
	}
	return nil
}

// waitFor takes the next CALL of action from the CSMS, checks it and answers it.
func (r *runner) waitFor(ctx context.Context, st *Step, action string, sr *types.StepResult) error {
	_, _ = fmt.Fprintf(r.errw, "waiting up to %s for %s from the CSMS\n", r.timeout(st), action)
	in, err := r.next(ctx, action, r.timeout(st))
	if err != nil {
		return err
	}
	sr.Request = in.req
	resp, rerr := r.answerFor(st, action)
	sr.Response = resp
	var ce *ocpp.CallError
	if errors.As(rerr, &ce) {
		sr.ErrorCode = string(ce.Code)
	}
	in.reply <- reply{resp: resp, err: rerr}
	if fails := check(in.req, st.Expect); len(fails) > 0 {
		return errors.New(strings.Join(fails, "; "))
	}
	if rerr != nil && ce == nil {
		return rerr
	}
	return r.save(in.req, st.Save)
}

func (r *runner) next(ctx context.Context, action string, timeout time.Duration) (*incoming, error) {
	r.mu.Lock()
	for i, in := range r.pending {
		if in.action == action {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			r.mu.Unlock()
			return in, nil
		}
	}
	held := len(r.pending) > 0
	var first string
	if held {
		first = r.pending[0].action
	}
	r.mu.Unlock()
	if held {
		return nil, outOfOrder(action, first)
	}
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("no %s from the CSMS within %s", action, timeout)
		case in := <-r.inbox:
			if in.action == action {
				return in, nil
			}
			// Held for the later step that waits for it.
			r.mu.Lock()
			r.pending = append(r.pending, in)
			r.mu.Unlock()
			return nil, outOfOrder(action, in.action)
		}
	}
}

// outOfOrder is the failure of a wait_for step when the CSMS sent the CALL of a later
// step first. OCPP-J lets a peer have one CALL outstanding at a time, so the CSMS cannot
// send the awaited one before that CALL is answered, and that answer belongs to the later
// step: waiting would only run into the timeout.
func outOfOrder(want, got string) error {
	return fmt.Errorf("the CSMS sent %s while this step waits for %s; a CSMS sends one CALL at a time, so %s cannot "+
		"arrive before %s is answered by its own (later) step: reorder the steps", got, want, want, got)
}

// answerFor builds the answer of a wait_for step, validated for the version.
func (r *runner) answerFor(st *Step, action string) (json.RawMessage, error) {
	a := st.Answer
	switch a.Behaviour {
	case "not-supported":
		return nil, ocpp.NewCallError(ocpp.NotSupported, "the scenario answers "+action+" with NotSupported")
	case "not-implemented":
		return nil, ocpp.NewCallError(ocpp.NotImplemented, "the scenario answers "+action+" with NotImplemented")
	case "accept", "reject":
		status := map[string]string{"accept": "Accepted", "reject": "Rejected"}[a.Behaviour]
		resp := json.RawMessage(`{"status":"` + status + `"}`)
		if ocppx.ValidateResponse(r.v, action, resp) != nil {
			return nil, fmt.Errorf("%s: %s has no status %s in OCPP %s", a.Behaviour, action, status, r.v)
		}
		return resp, nil
	}
	if a.JSON == nil {
		return defaultAnswer(r.v, action)
	}
	resp, err := r.payload(a.JSON)
	if err != nil {
		return nil, err
	}
	if verr := ocppx.ValidateResponse(r.v, action, resp); verr != nil {
		return nil, fmt.Errorf("the answer is not valid for OCPP %s: %w", r.v, verr)
	}
	return resp, nil
}

func defaultAnswer(v ocpp.Version, action string) (json.RawMessage, error) {
	for _, c := range []json.RawMessage{json.RawMessage(`{"status":"Accepted"}`), json.RawMessage(`{}`)} {
		if ocppx.ValidateResponse(v, action, c) == nil {
			return c, nil
		}
	}
	return nil, ocpp.NewCallError(ocpp.NotSupported, "the scenario gives no answer for "+action)
}

func (r *runner) expectNoCall(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.quiet, r.noise = true, nil
	r.mu.Unlock()
	_, _ = fmt.Fprintf(r.errw, "expecting no CALL from the CSMS for %s\n", d)
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quiet = false
	if len(r.noise) > 0 {
		return fmt.Errorf("the CSMS sent %s", strings.Join(r.noise, ", "))
	}
	return nil
}

// waited reports whether a step from the current one on waits for action.
func (r *runner) waited(action string) bool {
	for i := r.cursor; i < len(r.sc.Steps); i++ {
		if r.sc.Steps[i].WaitFor == action {
			return true
		}
	}
	return false
}

// dispatcher answers the CSMS's CALLs: through the wait_for step that waits for them, or
// with the default answer when no step does.
type dispatcher struct {
	r   *runner
	v   ocpp.Version
	def *answer.Default
}

func (d *dispatcher) Dispatch(ctx context.Context, p ocpp.Peer, action string, req json.RawMessage) (json.RawMessage, error) {
	r := d.r
	r.mu.Lock()
	if r.quiet {
		r.noise = append(r.noise, action)
	}
	waited := r.waited(action)
	r.mu.Unlock()
	if waited {
		if err := ocppx.ValidateRequest(d.v, action, req); err != nil {
			var ve *ocpp.ValidationError
			if errors.As(err, &ve) {
				return nil, ve.CallError()
			}
		}
		in := &incoming{action: action, req: req, reply: make(chan reply, 1), at: time.Now()}
		select {
		case r.inbox <- in:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.ended:
			return d.unexpected(ctx, p, action, req)
		}
		select {
		case rep := <-in.reply:
			return rep.resp, rep.err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.ended:
			// The scenario ended before the step that waits for it ran.
			return d.unexpected(ctx, p, action, req)
		}
	}
	return d.unexpected(ctx, p, action, req)
}

// unexpected answers a CALL that no step answers with the default answer and
// lists it in the result.
func (d *dispatcher) unexpected(ctx context.Context, p ocpp.Peer, action string, req json.RawMessage) (json.RawMessage, error) {
	r := d.r
	resp, err := d.def.Dispatch(ctx, p, action, req)
	rec := types.Received{Time: types.Millis(time.Now()), Action: action, Request: req, Response: resp}
	var ce *ocpp.CallError
	if errors.As(err, &ce) {
		rec.ErrorCode = string(ce.Code)
	}
	r.mu.Lock()
	r.unexpected = append(r.unexpected, rec)
	r.mu.Unlock()
	return resp, err
}

func (d *dispatcher) DispatchSend(_ context.Context, _ ocpp.Peer, action string, req json.RawMessage) error {
	r := d.r
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.quiet {
		r.noise = append(r.noise, action)
	}
	r.unexpected = append(r.unexpected, types.Received{Time: types.Millis(time.Now()), Action: action, Request: req})
	return nil
}
