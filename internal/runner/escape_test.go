// SPDX-License-Identifier: MIT

package runner

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/ocppctl/internal/errs"
)

// The error line holds what the peer wrote (a CALLERROR description): its control
// characters are escaped, on one line, as are the causes --debug shows.
func TestRenderFatalTextEscapes(t *testing.T) {
	evil := "x\x1b]0;pwned\x07\r\nerror: forged\u009b"
	err := errs.New(errs.KindNegative, errs.CodeCallError, "Heartbeat: the peer answered with CALLERROR GenericError: "+evil, errors.New(evil))
	var stdout, stderr bytes.Buffer
	if code := RenderFatal(&Invocation{}, err, &stdout, &stderr, true); code != errs.ExitProtocol {
		t.Fatalf("code %d", code)
	}
	out := stderr.String()
	if strings.ContainsAny(out, "\x1b\x07\r") || strings.Contains(out, "\x9b") {
		t.Fatalf("raw control characters on stderr: %q", out)
	}
	if lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "cause: ") {
		t.Fatalf("one error line and one cause line, got %q", out)
	}
}
