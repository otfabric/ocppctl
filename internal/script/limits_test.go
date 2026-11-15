// SPDX-License-Identifier: MIT

package script

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/readlimit"
)

// A scenario is read up to readlimit.Config bytes, and each of its payload and answer
// files up to readlimit.Payload bytes; more is invalid_config (exit 3), and reading stops
// at the limit.
func TestLoadLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "s.yaml")
	for _, n := range []int{readlimit.Config - 1, readlimit.Config, readlimit.Config + 1, 2 * readlimit.Config} {
		// A comment of n bytes: a scenario with nothing in it.
		if err := os.WriteFile(f, []byte("#"+strings.Repeat(" ", n-2)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := Load(f)
		runtime.ReadMemStats(&after)
		large := err != nil && strings.Contains(err.Error(), "larger than 4 MiB")
		if large != (n > readlimit.Config) || err == nil {
			t.Errorf("%d bytes: %v", n, err)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; n > readlimit.Config && alloc > 8*readlimit.Config {
			t.Errorf("%d bytes: allocated %d bytes, budget %d", n, alloc, 8*readlimit.Config)
		}
	}
	cfg := &Config{Target: conn.NewTarget(), File: f}
	cfg.URL, cfg.InsecureTransport = "ws://h/CS1", true
	if _, _, err := Prepare(cfg); err == nil {
		t.Error("Prepare accepted a scenario over the limit")
	} else if e, ok := errs.As(err); !ok || e.Code != errs.CodeInvalidConfig {
		t.Errorf("Prepare: %v, want invalid_config", err)
	}

	for _, n := range []int{readlimit.Payload, readlimit.Payload + 1} {
		p := filepath.Join(dir, "p.json")
		if err := os.WriteFile(p, []byte(`{"a":"`+strings.Repeat("x", n-8)+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("steps:\n  - call: Heartbeat\n    payload: \"@p.json\"\n  - wait_for: Reset\n    answer: \"@p.json\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(f)
		if got := strings.Count(errString(err), "larger than 68 KiB"); (n > readlimit.Payload) != (got == 2) {
			t.Errorf("payload file of %d bytes: %v", n, err)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
