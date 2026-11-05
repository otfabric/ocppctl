// SPDX-License-Identifier: MIT

package offline

import (
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp/ocppj"

	"github.com/otfabric/ocppctl/internal/errs"
)

// endless is stdin that never ends, as `yes | ocppctl frame decode -`.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// A frame on stdin is read up to the largest frame OCPP-J allows, not without bound: an
// endless or huge input is refused instead of filling memory.
func TestFrameDecodeBoundedStdin(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := FrameDecode(&FrameDecodeConfig{Version: "1.6", Frame: "-"}, endless{})
		done <- err
	}()
	select {
	case err := <-done:
		if e, ok := errs.As(err); !ok || e.Code != errs.CodeProtocolViolation {
			t.Fatalf("an endless frame: %v, want protocol_violation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("frame decode - reads an endless stdin for ever")
	}
	// The largest frame still decodes, with whitespace around it.
	payload := `{"vendorId":"` + strings.Repeat("x", 255) + `"}`
	frame := `[2,"1","DataTransfer",` + payload + `]`
	frame += strings.Repeat(" ", ocppj.DefaultMaxFrameSize-len(frame))
	res, err := FrameDecode(&FrameDecodeConfig{Version: "1.6", Frame: "-"}, strings.NewReader("\n"+frame+"\n"))
	if err != nil || res.Action != "DataTransfer" {
		t.Fatalf("a frame of the largest size: %+v, %v", res, err)
	}
}

// failingReader is stdin that cannot be read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("stdin closed") }

func code(err error) string {
	if e, ok := errs.As(err); ok {
		return e.Code
	}
	return "untyped: " + fmt.Sprint(err)
}

func TestOfflineErrors(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"validate: version": {second(Validate(&ValidateConfig{Version: "9", Action: "Heartbeat", Payload: "{}"}, nil)), errs.CodeUnsupportedVersion},
		"validate: stdin":   {second(Validate(&ValidateConfig{Version: "1.6", Action: "Heartbeat", Payload: "-"}, failingReader{})), errs.CodeInputFileOpenFailed},
		"validate: send":    {second(Validate(&ValidateConfig{Version: "2.1", Action: "NotifyPeriodicEventStream", Payload: "{}", Response: true}, nil)), errs.CodeInvalidFlagCombination},
		"decode: version":   {second(FrameDecode(&FrameDecodeConfig{Version: "x", Frame: "[]"}, nil)), errs.CodeUnsupportedVersion},
		"decode: stdin":     {second(FrameDecode(&FrameDecodeConfig{Version: "1.6", Frame: "-"}, failingReader{})), errs.CodeInputFileOpenFailed},
		"decode: malformed": {second(FrameDecode(&FrameDecodeConfig{Version: "1.6", Frame: `[2,"1"]`}, nil)), errs.CodeProtocolViolation},
		"encode: version":   {second(FrameEncode(&FrameEncodeConfig{Version: "x"}, nil)), errs.CodeUnsupportedVersion},
		"encode: type":      {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "ping"}, nil)), errs.CodeInvalidInput},
		"encode: SEND 1.6":  {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "send", ID: "1"}, nil)), errs.CodeUnsupportedVersion},
		"encode: no id":     {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "call"}, nil)), errs.CodeInvalidInput},
		"encode: no action": {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "call", ID: "1"}, nil)), errs.CodeInvalidInput},
		"encode: payload":   {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "callresult", ID: "1", Payload: "-"}, failingReader{})), errs.CodeInputFileOpenFailed},
		"encode: no code":   {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "callerror", ID: "1"}, nil)), errs.CodeInvalidInput},
		"encode: bad code":  {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "callerror", ID: "1", ErrorCode: "Nope"}, nil)), errs.CodeInvalidInput},
		"encode: details":   {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "callerror", ID: "1", ErrorCode: "GenericError", Details: "[1]"}, nil)), errs.CodeInvalidPayload},
		"encode: long id":   {second(FrameEncode(&FrameEncodeConfig{Version: "1.6", Type: "callresult", ID: strings.Repeat("9", 37)}, nil)), errs.CodeInvalidInput},
		"cert: hash":        {second(CertHash(&CertHashConfig{Hash: "md5"})), errs.CodeInvalidInput},
		"cert: no cert":     {second(CertHash(&CertHashConfig{Hash: "sha256"})), errs.CodeInvalidInput},
		"cert: missing":     {second(CertHash(&CertHashConfig{Hash: "sha256", Cert: "/nonexistent.pem"})), errs.CodeInputFileOpenFailed},
	} {
		if got := code(c.err); got != c.want {
			t.Errorf("%s: %s (%v), want %s", name, got, c.err, c.want)
		}
	}
	// A PEM block that claims to be a certificate and is not one, and a file without one.
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.pem")
	_ = os.WriteFile(broken, append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2}})...), 0o600)
	if got := code(second(CertHash(&CertHashConfig{Hash: "sha256", Cert: broken}))); got != errs.CodeInputDecodeFailed {
		t.Errorf("a broken certificate: %s", got)
	}
	none := filepath.Join(dir, "none.pem")
	_ = os.WriteFile(none, []byte("nothing"), 0o600)
	if got := code(second(CertHash(&CertHashConfig{Hash: "sha256", Cert: none}))); got != errs.CodeInputDecodeFailed {
		t.Errorf("no certificate: %s", got)
	}
	if got := code(second(CertHash(&CertHashConfig{Hash: "sha256", Cert: none, Issuer: none}))); got != errs.CodeInputDecodeFailed {
		t.Errorf("no certificate, with issuer: %s", got)
	}
}

func second[T any](_ T, err error) error { return err }

// A CALL of an action the version does not have decodes, with the violation.
func TestFrameDecodeUnknownAction(t *testing.T) {
	res, err := FrameDecode(&FrameDecodeConfig{Version: "1.6", Frame: `[2,"1","Nope",{}]`}, nil)
	if err != nil || res.PayloadValid == nil || *res.PayloadValid || res.Violation == nil || res.Violation.ErrorCode == "" {
		t.Fatalf("%+v, %v", res, err)
	}
}
