// SPDX-License-Identifier: MIT

package station

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/types"
)

// RunConfig configures `client run`.
type RunConfig struct {
	conn.Target
	Config       string `flag:"config" env:"OCPPCTL_CONFIG" desc:"Station model, YAML or JSON (default: the built-in model, see \"client generate-config\")"`
	Duration     string `flag:"duration" env:"OCPPCTL_DURATION" desc:"Stop after this long (0: run until interrupted)"`
	NoReconnect  bool   `flag:"no-reconnect" desc:"Exit when the connection is lost instead of reconnecting with the OCPP back-off"`
	PingInterval string `flag:"ping-interval" env:"OCPPCTL_PING_INTERVAL" desc:"WebSocket ping interval (0: no pings)"`
	Stagger      string `flag:"stagger" desc:"Delay between the start of one station and the next, with a {N..M} identity range"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
	Debug        bool   `flag:"-"`
}

// NewRunConfig returns the defaults.
func NewRunConfig() RunConfig {
	return RunConfig{Target: conn.NewTarget(), Duration: "0", PingInterval: "0", Stagger: "0", OutputFormat: "text"}
}

// MaxStations bounds an identity range.
const MaxStations = 10000

var rangeRE = regexp.MustCompile(`\{(\d+)\.\.(\d+)\}`)

// ExpandURL expands one {N..M} range in the URL into a URL per station. The width of N
// pads the numbers: {001..100} gives 001, 002, ... 100.
func ExpandURL(u string) ([]string, error) {
	locs := rangeRE.FindAllStringSubmatchIndex(u, -1)
	switch len(locs) {
	case 0:
		return []string{u}, nil
	case 1:
	default:
		return nil, errs.InvalidInput(errs.CodeInvalidURL, "--url may hold one {N..M} identity range", nil)
	}
	l := locs[0]
	from, _ := strconv.Atoi(u[l[2]:l[3]])
	to, _ := strconv.Atoi(u[l[4]:l[5]])
	width := l[3] - l[2]
	if to < from || to-from+1 > MaxStations {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, fmt.Sprintf("invalid identity range {%d..%d}: up to %d stations, ascending", from, to, MaxStations), nil)
	}
	out := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, u[:l[0]]+fmt.Sprintf("%0*d", width, n)+u[l[1]:])
	}
	return out, nil
}

// Prepared is a validated RunConfig.
type Prepared struct {
	Targets      []*conn.Parsed
	Model        *Model
	Duration     time.Duration
	PingInterval time.Duration
	Stagger      time.Duration
}

func nonNegative(flag, s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < 0 {
		return 0, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid %s %q: want a duration such as 30s, or 0", flag, s), err)
	}
	return d, nil
}

// Prepare checks everything before a station connects.
func Prepare(cfg *RunConfig) (*Prepared, error) {
	urls, err := ExpandURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	p := &Prepared{}
	for _, u := range urls {
		t := cfg.Target
		t.URL = u
		pt, err := t.Parse()
		if err != nil {
			return nil, err
		}
		p.Targets = append(p.Targets, pt)
	}
	if p.Duration, err = nonNegative("--duration", cfg.Duration); err != nil {
		return nil, err
	}
	if p.PingInterval, err = nonNegative("--ping-interval", cfg.PingInterval); err != nil {
		return nil, err
	}
	if p.Stagger, err = nonNegative("--stagger", cfg.Stagger); err != nil {
		return nil, err
	}
	versions := p.Targets[0].Versions
	if cfg.Config == "" {
		p.Model = Demo()
		if err := p.Model.Validate(versions); err != nil {
			return nil, errs.Internal(err)
		}
	} else if p.Model, err = Load(cfg.Config, versions); err != nil {
		code := errs.CodeInvalidConfig
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			code = errs.CodeInputFileOpenFailed
		}
		return nil, errs.InvalidInput(code, err.Error(), err)
	}
	return p, nil
}

// Run runs every station until ctx ends or --duration passes, and writes a summary to
// stderr. It returns ExitOK when every station ran, ExitPartial when some failed, and the
// error of the first when all failed.
func Run(ctx context.Context, cfg *RunConfig, p *Prepared, emit func(types.StationEvent), stderr io.Writer) (int, error) {
	if p.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Duration)
		defer cancel()
	}
	stations := make([]*Station, len(p.Targets))
	failures := make([]error, len(p.Targets))
	var wg sync.WaitGroup
	for i, t := range p.Targets {
		if i > 0 && p.Stagger > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(p.Stagger):
			}
		}
		if ctx.Err() != nil {
			failures[i] = ctx.Err()
			stations[i] = nil
			continue
		}
		s := New(Options{Parsed: t, Model: p.Model, Reconnect: !cfg.NoReconnect, PingInterval: p.PingInterval,
			Debug: cfg.Debug, Stderr: stderr, Emit: emit})
		stations[i] = s
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Run(ctx); err != nil {
				failures[i] = err
				emit(types.StationEvent{Time: types.Millis(time.Now()), Station: t.Identity, Event: "error", Message: err.Error()})
			}
		}()
	}
	wg.Wait()
	failed, ran := 0, 0
	var first error
	for i, err := range failures {
		if stations[i] == nil {
			continue
		}
		ran++
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if len(stations) > 1 {
		summary(stderr, stations, failures, ran)
	}
	switch {
	case ran > 0 && failed == ran:
		return 0, first
	case failed > 0:
		return errs.ExitPartial, nil
	}
	return errs.ExitOK, nil
}

func summary(w io.Writer, stations []*Station, failures []error, started int) {
	var calls, errCount, booted, sessions int
	var total, worst time.Duration
	var failedIDs []string
	for i, s := range stations {
		if s == nil {
			continue
		}
		st := s.Stats()
		calls += st.Calls
		errCount += st.Errors
		sessions += st.Sessions
		total += st.Total
		worst = max(worst, st.Max)
		if st.Booted {
			booted++
		}
		if failures[i] != nil {
			failedIDs = append(failedIDs, st.Identity)
		}
	}
	sort.Strings(failedIDs)
	avg := time.Duration(0)
	if calls > 0 {
		avg = total / time.Duration(calls)
	}
	_, _ = fmt.Fprintf(w, "\nsummary: %d stations, %d booted, %d failed; %d calls, %d errors, latency avg %s max %s; %d sessions\n",
		started, booted, len(failedIDs), calls, errCount, avg.Round(time.Millisecond), worst.Round(time.Millisecond), sessions)
	if len(failedIDs) > 0 {
		_, _ = fmt.Fprintf(w, "failed: %s\n", strings.Join(failedIDs, ", "))
	}
}
