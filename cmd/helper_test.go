// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is an output buffer that is safe for concurrent writers: streaming commands
// write from the goroutines of the connection.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// result is the outcome of one invocation of ocppctl.
type result struct {
	stdout, stderr string
	code           int
}

// run executes ocppctl with args, as the process would.
func run(t testing.TB, args ...string) result {
	t.Helper()
	return runIn(t, context.Background(), "", args...)
}

// runIn executes ocppctl with stdin.
func runIn(t testing.TB, ctx context.Context, stdin string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stdout, stderr syncBuffer
	code := Run(ctx, args, strings.NewReader(stdin), &stdout, &stderr)
	return result{stdout.String(), stderr.String(), code}
}

// want fails the test unless the exit code is code.
func (r result) want(t testing.TB, code int) result {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
	return r
}

// has fails the test unless stdout contains every one of subs.
func (r result) has(t testing.TB, subs ...string) result {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("stdout does not contain %q:\n%s", s, r.stdout)
		}
	}
	return r
}

// errHas fails the test unless stderr contains every one of subs.
func (r result) errHas(t testing.TB, subs ...string) result {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr does not contain %q:\n%s", s, r.stderr)
		}
	}
	return r
}

// decode parses stdout as one JSON document into v.
func (r result) decode(t testing.TB, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not the expected JSON: %v\n%s", err, r.stdout)
	}
}

// lines parses stdout as JSON Lines.
func (r result) lines(t testing.TB) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// errorCode returns the code of the JSON error object on stdout.
func (r result) errorCode(t testing.TB) (kind, code string) {
	t.Helper()
	var env struct {
		Error struct{ Kind, Code, Message string }
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil || env.Error.Code == "" {
		t.Fatalf("stdout is not a JSON error object: %v\n%s", err, r.stdout)
	}
	return env.Error.Kind, env.Error.Code
}

// eventually polls cond for up to fifteen seconds: long enough for a loaded CI runner
// with the race detector, and only ever reached when the test fails.
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// live is an invocation of ocppctl that is still running: a test waits for what it has
// written so far instead of sleeping.
type live struct {
	stdout, stderr *syncBuffer
	done           chan result
	cancel         context.CancelFunc
}

// start runs ocppctl with args in the background; stop (or the end of the test) cancels it.
func start(t testing.TB, args ...string) *live {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	l := &live{stdout: &syncBuffer{}, stderr: &syncBuffer{}, done: make(chan result, 1), cancel: cancel}
	go func() {
		code := Run(ctx, args, strings.NewReader(""), l.stdout, l.stderr)
		l.done <- result{l.stdout.String(), l.stderr.String(), code}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-l.done:
		case <-time.After(30 * time.Second):
			t.Errorf("ocppctl %v did not stop", args)
		}
	})
	return l
}

// waitStdout waits until stdout contains s.
func (l *live) waitStdout(t testing.TB, s string) {
	t.Helper()
	eventually(t, "stdout to contain "+s, func() bool { return strings.Contains(l.stdout.String(), s) })
}

// waitStderr waits until stderr contains s.
func (l *live) waitStderr(t testing.TB, s string) {
	t.Helper()
	eventually(t, "stderr to contain "+s, func() bool { return strings.Contains(l.stderr.String(), s) })
}

// result waits for the invocation to end by itself.
func (l *live) result(t testing.TB) result {
	t.Helper()
	select {
	case r := <-l.done:
		l.done <- r // for the cleanup
		return r
	case <-time.After(45 * time.Second):
		t.Fatalf("ocppctl did not end\nstdout:\n%s\nstderr:\n%s", l.stdout.String(), l.stderr.String())
		return result{}
	}
}

// stop cancels the invocation (Ctrl-C) and returns its result.
func (l *live) stop(t testing.TB) result {
	t.Helper()
	l.cancel()
	return l.result(t)
}
