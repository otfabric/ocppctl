// SPDX-License-Identifier: MIT

package csms

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/readlimit"
)

// A CSMS model file is read up to readlimit.Config bytes: a larger one is invalid_config
// (exit 3), and reading it stops at the limit.
func TestLoadLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "m.yaml")
	for _, n := range []int{readlimit.Config - 1, readlimit.Config, readlimit.Config + 1, 2 * readlimit.Config} {
		// A comment of n bytes: a model with nothing in it.
		if err := os.WriteFile(f, []byte("#"+strings.Repeat(" ", n-2)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := Load(f, []ocpp.Version{ocpp.V16})
		runtime.ReadMemStats(&after)
		large := err != nil && strings.Contains(err.Error(), "larger than 4 MiB")
		if large != (n > readlimit.Config) || err == nil {
			t.Errorf("%d bytes: %v", n, err)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; n > readlimit.Config && alloc > 8*readlimit.Config {
			t.Errorf("%d bytes: allocated %d bytes, budget %d", n, alloc, 8*readlimit.Config)
		}
	}
	cfg := plainConfig()
	cfg.Config = f
	if e, ok := errs.As(Check(&cfg)); !ok || e.Kind != errs.KindInvalidInput || e.Code != errs.CodeInvalidConfig {
		t.Errorf("Check: %v, want invalid_config", Check(&cfg))
	}
}

// --rate-limit is a number of messages a second from 0 (no limit) to MaxRateLimit: NaN,
// an infinity, a negative number or a larger one is invalid input (exit 3), and the
// default burst is the rate rounded up.
func TestRateLimitRange(t *testing.T) {
	for _, tc := range []struct {
		rate  float64
		burst int
		ok    bool
		want  int // the burst
	}{
		{math.NaN(), 0, false, 0},
		{math.Inf(1), 0, false, 0},
		{math.Inf(-1), 0, false, 0},
		{-1, 0, false, 0},
		{0, 0, true, 0},
		{0, 5, true, 5},
		{1e-300, 0, true, 1},
		{0.5, 0, true, 1},
		{1, 0, true, 1},
		{2.5, 0, true, 3},
		{2.5, 7, true, 7},
		{MaxRateLimit - 1, 0, true, MaxRateLimit - 1},
		{MaxRateLimit, 0, true, MaxRateLimit},
		{MaxRateLimit + 1, 0, false, 0},
		{math.Nextafter(MaxRateLimit, math.Inf(1)), 0, false, 0},
		{1e19, 0, false, 0},
		{math.MaxFloat64, 0, false, 0},
	} {
		cfg := plainConfig()
		cfg.RateLimit, cfg.RateBurst = tc.rate, tc.burst
		p, err := cfg.plan()
		if !tc.ok {
			if e, ok := errs.As(err); !ok || e.Kind != errs.KindInvalidInput || errs.ExitCodeForKind(e.Kind) != 3 {
				t.Errorf("--rate-limit %v: %v, want invalid input", tc.rate, err)
			}
			continue
		}
		if err != nil || p.rate != tc.rate || p.burst != tc.want {
			t.Errorf("--rate-limit %v --rate-burst %d: %+v, %v, want the burst %d", tc.rate, tc.burst, p, err, tc.want)
			continue
		}
		// go-ocpp takes what plan computed.
		if _, err := server.New((&CSMS{plan: p}).options(&cfg, nil)...); err != nil {
			t.Errorf("--rate-limit %v: go-ocpp refuses %v", tc.rate, err)
		}
	}
}
