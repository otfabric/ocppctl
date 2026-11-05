// SPDX-License-Identifier: MIT

package conn

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/errs"
)

// errText is everything a fatal error shows: its message and, with --debug, its causes.
func errText(err error) string {
	var b strings.Builder
	for e := err; e != nil; e = errors.Unwrap(e) {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// A password written into --url is refused without being repeated: the error goes to
// stderr, to the JSON error object and to CI logs.
func TestParseDoesNotRepeatCredentials(t *testing.T) {
	for _, u := range []string{
		"wss://CS1:s3cretpw@csms.example.com/ocpp/CS1",
		"http://CS1:s3cretpw@csms.example.com/ocpp/CS1",
		"ws://CS1:s3cretpw@csms.example.com:badport/ocpp/CS1",
		"wss://CS1:s3cretpw@/ocpp/CS1",
		"CS1:s3cretpw@csms.example.com/ocpp/CS1",
		"wss://CS1:s3cretpw@csms.example.com/ocpp/%zz",
	} {
		_, err := Target{URL: u, Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s"}.Parse()
		e, ok := errs.As(err)
		if !ok || e.Kind != errs.KindInvalidInput || e.Code != errs.CodeInvalidURL {
			t.Errorf("%s: %v, want invalid_url", u, err)
			continue
		}
		if strings.Contains(errText(err), "s3cretpw") {
			t.Errorf("%s: the password is in the error:\n%s", u, errText(err))
		}
	}
}

// A malformed --header may hold a token ("Authorization: Bearer …"): the error names
// the flag, not the value.
func TestParseDoesNotRepeatHeaderValue(t *testing.T) {
	for _, h := range []string{"Authorization: Bearer s3cret-token", "=s3cret-token", " =s3cret-token"} {
		_, err := Target{URL: "ws://h/CS1", Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s",
			InsecureTransport: true, Headers: []string{h}}.Parse()
		if e, ok := errs.As(err); !ok || e.Code != errs.CodeInvalidInput {
			t.Errorf("%q: %v", h, err)
			continue
		}
		if strings.Contains(errText(err), "s3cret-token") {
			t.Errorf("%q: the value is in the error: %v", h, err)
		}
	}
}

// A CSMS that accepts the TCP connection and never answers the handshake does not hold
// the command beyond --connect-timeout.
func TestDialSilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	p, err := Target{URL: fmt.Sprintf("ws://%s/CS1", ln.Addr()), Versions: []string{"1.6"}, Timeout: "1s",
		ConnectTimeout: "150ms", InsecureTransport: true}.Parse()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = Dial(context.Background(), p, Options{})
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Dial took %s with --connect-timeout 150ms", took)
	}
	if e, ok := errs.As(err); !ok || e.Kind != errs.KindTransport {
		t.Fatalf("a silent CSMS: %v, want a transport error", err)
	}
	// Interrupted while connecting: interrupted, not a transport error.
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	p.ConnectTimeout = time.Minute
	_, err = Dial(ctx, p, Options{})
	if e, ok := errs.As(err); !ok || e.Kind != errs.KindInterrupted {
		t.Fatalf("interrupted while connecting: %v", err)
	}
}

// The --debug trace writes what the peer sent; control characters in it are shown
// escaped, so that a CSMS cannot drive the operator's terminal.
func TestTraceEscapesControlCharacters(t *testing.T) {
	var b bytes.Buffer
	obs := Trace(&b)
	obs(ocpp.ConnInfo{StationID: "CS\x1b]0;x\x07"}, ocpp.Incoming, []byte("[3,\"1\",{\"a\":\"\x1b[2J\x9b1m\u009b2m\r\n> forged\"}]"))
	out := b.String()
	if strings.ContainsAny(out, "\x1b\x07\r") || strings.Contains(out, "\x9b") || strings.Count(out, "\n") != 1 {
		t.Fatalf("raw control characters in the trace: %q", out)
	}
	if !strings.Contains(out, `\x1b[2J`) {
		t.Fatalf("the escape is not visible: %q", out)
	}
}

func TestReadPayload(t *testing.T) {
	for arg, code := range map[string]string{
		"[1]":                        errs.CodeInvalidPayload,
		"nope":                       errs.CodeInputDecodeFailed,
		"@/nonexistent/payload.json": errs.CodeInputFileOpenFailed,
	} {
		if _, err := ReadPayload(arg, strings.NewReader("")); err == nil {
			t.Errorf("%s: no error", arg)
		} else if e, ok := errs.As(err); !ok || e.Code != code {
			t.Errorf("%s: %v, want %s", arg, err, code)
		}
	}
	if got, err := ReadPayload("-", strings.NewReader(`{"a":1}`)); err != nil || string(got) != `{"a":1}` {
		t.Errorf("stdin: %s, %v", got, err)
	}
	if got, err := ReadPayload("", nil); err != nil || string(got) != `{}` {
		t.Errorf("empty: %s, %v", got, err)
	}
}

// pemFiles writes a self-signed certificate and its key, and returns their paths.
func pemFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CS1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestParseTLS(t *testing.T) {
	cert, key := pemFiles(t)
	notPEM := filepath.Join(t.TempDir(), "x.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Target{URL: "wss://csms.example.com/ocpp/CS1", Versions: []string{"2.0.1"}, Timeout: "1s", ConnectTimeout: "1s"}
	ok := base
	ok.TLSCA, ok.TLSCert, ok.TLSKey, ok.TLSServerName = cert, cert, key, "csms"
	p, err := ok.Parse()
	if err != nil || p.TLS == nil || p.TLS.RootCAs == nil || len(p.TLS.Certificates) != 1 || p.TLS.ServerName != "csms" || p.TLS.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS: %+v, %v", p, err)
	}
	for name, c := range map[string]struct {
		mod  func(*Target)
		code string
	}{
		"CA file missing":   {func(t *Target) { t.TLSCA = "/nonexistent/ca.pem" }, errs.CodeInputFileOpenFailed},
		"CA file not PEM":   {func(t *Target) { t.TLSCA = notPEM }, errs.CodeInputDecodeFailed},
		"cert without key":  {func(t *Target) { t.TLSCert = cert }, errs.CodeInvalidFlagCombination},
		"key without cert":  {func(t *Target) { t.TLSKey = key }, errs.CodeInvalidFlagCombination},
		"key is not a key":  {func(t *Target) { t.TLSCert, t.TLSKey = cert, cert }, errs.CodeInputDecodeFailed},
		"TLS flag with ws":  {func(t *Target) { t.URL, t.TLSServerName = "ws://h/CS1", "h" }, errs.CodeInvalidFlagCombination},
		"ws without opt-in": {func(t *Target) { t.URL = "ws://h/CS1" }, errs.CodeInvalidFlagCombination},
		"no URL":            {func(t *Target) { t.URL = " " }, errs.CodeInvalidURL},
		"no identity":       {func(t *Target) { t.URL = "wss://h/ocpp/" }, errs.CodeInvalidURL},
		"bad version":       {func(t *Target) { t.Versions = []string{"3"} }, errs.CodeInvalidInput},
		"bad timeout":       {func(t *Target) { t.Timeout = "0s" }, errs.CodeInvalidInput},
		"bad connect":       {func(t *Target) { t.ConnectTimeout = "soon" }, errs.CodeInvalidInput},
		"no password file":  {func(t *Target) { t.PasswordFile = "/nonexistent/pw" }, errs.CodeInputFileOpenFailed},
	} {
		tg := base
		c.mod(&tg)
		_, err := tg.Parse()
		if e, ok := errs.As(err); !ok || e.Code != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	// The key's content never shows up in an error.
	tg := base
	tg.TLSCert, tg.TLSKey = key, key
	if _, err := tg.Parse(); err == nil || strings.Contains(errText(err), "PRIVATE KEY-----\n") {
		t.Fatalf("key in the error: %v", err)
	}
}

func TestPasswordAndHeaders(t *testing.T) {
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte("s3cret\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tg := Target{URL: "ws://h/ocpp/CS%201", Versions: []string{"1.6"}, Timeout: "1s", ConnectTimeout: "1s", InsecureTransport: true,
		PasswordFile: pw, Headers: []string{" X-Test =a=b"}}
	p, err := tg.Parse()
	if err != nil || p.Password != "s3cret" || p.Identity != "CS 1" || len(p.Headers) != 1 || p.Headers[0] != [2]string{"X-Test", "a=b"} {
		t.Fatalf("%+v, %v", p, err)
	}
	t.Setenv(PasswordEnv, "fromenv")
	tg.PasswordFile = ""
	if p, err := tg.Parse(); err != nil || p.Password != "fromenv" {
		t.Fatalf("password from the environment: %+v, %v", p, err)
	}
	h := Handshake{Extensions: "permessage-deflate; client_no_context_takeover"}
	if !h.Compressed() || (&Handshake{}).Compressed() {
		t.Fatal("Compressed")
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"wss://u:p@h/x":        "wss://REDACTED@h/x",
		"u:p@h/x@y":            "REDACTED@h/x@y",
		"ws://h/p@x":           "ws://h/p@x",
		"ws://a@b@h:1?q=@":     "ws://REDACTED@h:1?q=@",
		"plain":                "plain",
		"wss://h#frag@x":       "wss://h#frag@x",
		"wss://user@host:port": "wss://REDACTED@host:port",
	} {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}
