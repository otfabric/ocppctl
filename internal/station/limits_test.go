// SPDX-License-Identifier: MIT

package station

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/readlimit"
)

// A model file is read up to readlimit.Config bytes: a larger one is invalid_config
// (exit 3), and reading it stops at the limit.
func TestLoadLimit(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{readlimit.Config - 1, readlimit.Config, readlimit.Config + 1, 2 * readlimit.Config} {
		f := filepath.Join(dir, "m.yaml")
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
	cfg := NewRunConfig()
	cfg.URL, cfg.InsecureTransport, cfg.Config = "ws://h/CS1", true, filepath.Join(dir, "m.yaml")
	_, err := Prepare(&cfg)
	if e, ok := errs.As(err); !ok || e.Kind != errs.KindInvalidInput || e.Code != errs.CodeInvalidConfig {
		t.Errorf("Prepare: %v, want invalid_config", err)
	}
}

// power_w and remote_power_w are numbers from 0 to MaxPowerW.
func TestPowerRange(t *testing.T) {
	for _, tc := range []struct {
		w  float64
		ok bool
	}{
		{math.NaN(), false}, {math.Inf(1), false}, {math.Inf(-1), false}, {-1, false}, {math.Copysign(0, -1), true},
		{0, true}, {1, true}, {11000, true}, {MaxPowerW - 1, true}, {MaxPowerW, true},
		{math.Nextafter(MaxPowerW, math.Inf(1)), false}, {MaxPowerW + 1, false}, {1e19, false}, {math.MaxFloat64, false},
	} {
		m := Demo()
		m.Sessions[0].PowerW = tc.w
		err := m.Validate([]ocpp.Version{ocpp.V16})
		if (err == nil) != tc.ok || (err != nil && !strings.Contains(err.Error(), "power_w must be a number from 0 to 10000000")) {
			t.Errorf("power_w %v: %v", tc.w, err)
		}
		m = Demo()
		m.RemotePowerW = tc.w
		err = m.Validate([]ocpp.Version{ocpp.V16})
		if (err == nil) != tc.ok || (err != nil && !strings.Contains(err.Error(), "remote_power_w must be a number from 0 to 10000000")) {
			t.Errorf("remote_power_w %v: %v", tc.w, err)
		}
	}
}
