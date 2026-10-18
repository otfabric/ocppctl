// SPDX-License-Identifier: MIT

package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/types"
)

func TestActions(t *testing.T) {
	var res types.ActionsResult
	run(t, "actions", "--ocpp-version", "2.1", "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if res.Version != "2.1" || len(res.Actions) < 70 {
		t.Fatalf("2.1 actions: %d", len(res.Actions))
	}
	sends := 0
	for _, a := range res.Actions {
		if a.Kind == "send" {
			sends++
		}
	}
	if sends == 0 {
		t.Fatal("OCPP 2.1 has SENDs")
	}
	run(t, "actions", "--ocpp-version", "1.6", "--from", "csms").want(t, errs.ExitOK).has(t, "RemoteStartTransaction", "DataTransfer")
	r := run(t, "actions", "--ocpp-version", "1.6", "--from", "station", "--format", "table").want(t, errs.ExitOK).has(t, "ACTION", "BootNotification")
	if strings.Contains(r.stdout, "RemoteStartTransaction") {
		t.Fatal("--from station lists a CSMS action")
	}
	run(t, "actions", "--ocpp-version", "3.0").want(t, errs.ExitInvalidInput)
	run(t, "actions", "--from", "nobody").want(t, errs.ExitInvalidInput)
}

func TestValidate(t *testing.T) {
	run(t, "validate", "BootNotification", "--ocpp-version", "2.0.1",
		"--payload", `{"reason":"PowerUp","chargingStation":{"vendorName":"V","model":"M"}}`).want(t, errs.ExitOK).has(t, "valid")
	var res types.ValidateResult
	run(t, "validate", "BootNotification", "--ocpp-version", "1.6", "--payload", `{"chargePointVendor":"V"}`, "--format", "json").
		want(t, errs.ExitInvalidInput).decode(t, &res)
	if res.Valid || len(res.Violations) != 1 || res.Violations[0].Field != "chargePointModel" {
		t.Fatalf("violations: %+v", res)
	}
	runIn(t, t.Context(), `{"currentTime":"2026-01-01T00:00:00Z"}`, "validate", "Heartbeat", "--ocpp-version", "1.6", "--response", "--payload", "-").
		want(t, errs.ExitOK)
	f := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(f, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "validate", "Heartbeat", "--ocpp-version", "2.1", "--payload", "@"+f, "--format", "table").want(t, errs.ExitOK)
	run(t, "validate", "Nope", "--ocpp-version", "2.1", "--payload", "{}").want(t, errs.ExitInvalidInput).errHas(t, "Nope")
	run(t, "validate", "heartbeat", "--ocpp-version", "2.1", "--payload", "{}").want(t, errs.ExitInvalidInput).errHas(t, "Heartbeat")
	run(t, "validate", "Heartbeat", "--payload", "[1]").want(t, errs.ExitInvalidInput)
	run(t, "validate", "Heartbeat", "--payload", "{").want(t, errs.ExitInvalidInput)
	run(t, "validate", "Heartbeat").want(t, errs.ExitUsage)
	run(t, "validate", "NotifyPeriodicEventStream", "--ocpp-version", "2.1", "--response", "--payload", "{}").want(t, errs.ExitInvalidInput)
}

func TestFrame(t *testing.T) {
	var res types.FrameResult
	run(t, "frame", "decode", `[2,"19223201","BootNotification",{"chargePointVendor":"V","chargePointModel":"M"}]`, "--ocpp-version", "1.6", "--format", "json").
		want(t, errs.ExitOK).decode(t, &res)
	if res.Type != "CALL" || res.TypeNumber != 2 || res.Action != "BootNotification" || res.PayloadValid == nil || !*res.PayloadValid {
		t.Fatalf("decode: %+v", res)
	}
	run(t, "frame", "decode", `[2,"1","BootNotification",{}]`, "--ocpp-version", "1.6").want(t, errs.ExitOK).has(t, "invalid", "chargePointVendor")
	run(t, "frame", "decode", `[2,"1","Bogus",{}]`, "--ocpp-version", "1.6", "--format", "table").want(t, errs.ExitOK).has(t, "Bogus")
	run(t, "frame", "decode", `[4,"1","NotImplemented","no",{}]`, "--ocpp-version", "2.0.1").want(t, errs.ExitOK).has(t, "NotImplemented")
	runIn(t, t.Context(), "[3,\"1\",{}]\n", "frame", "decode", "-", "--ocpp-version", "2.0.1").want(t, errs.ExitOK).has(t, "CALLRESULT")
	run(t, "frame", "decode", `[6,"1","NotifyPeriodicEventStream",{}]`, "--ocpp-version", "2.0.1").want(t, errs.ExitInvalidInput)
	run(t, "frame", "decode", `[6,"1","NotifyPeriodicEventStream",{}]`, "--ocpp-version", "2.1", "--no-validate").want(t, errs.ExitOK)
	run(t, "frame", "decode", `not json`).want(t, errs.ExitInvalidInput)
	run(t, "frame").want(t, errs.ExitOK).has(t, "CALLRESULTERROR")

	run(t, "frame", "encode", "--type", "call", "--id", "1", "--action", "Heartbeat", "--ocpp-version", "1.6", "--format", "json").
		want(t, errs.ExitOK).has(t, `"frame": "[2,\"1\",\"Heartbeat\",{}]"`)
	run(t, "frame", "encode", "--type", "callresult", "--id", "1", "--payload", `{"currentTime":"x"}`).want(t, errs.ExitOK).has(t, `[3,"1",{"currentTime":"x"}]`)
	run(t, "frame", "encode", "--type", "callerror", "--id", "1", "--error-code", "NotImplemented", "--description", "d", "--details", `{"a":1}`).
		want(t, errs.ExitOK).has(t, `[4,"1","NotImplemented","d",{"a":1}]`)
	run(t, "frame", "encode", "--type", "callresulterror", "--id", "1", "--error-code", "GenericError", "--ocpp-version", "2.1", "--format", "table").want(t, errs.ExitOK)
	run(t, "frame", "encode", "--type", "callresulterror", "--id", "1", "--error-code", "GenericError", "--ocpp-version", "1.6").want(t, errs.ExitInvalidInput)
	run(t, "frame", "encode", "--type", "send", "--id", "1", "--action", "NotifyPeriodicEventStream", "--ocpp-version", "2.1").want(t, errs.ExitOK)
	run(t, "frame", "encode", "--type", "call", "--id", "1").want(t, errs.ExitInvalidInput).errHas(t, "--action")
	run(t, "frame", "encode", "--type", "callerror", "--id", "1").want(t, errs.ExitInvalidInput).errHas(t, "--error-code")
	run(t, "frame", "encode", "--type", "callerror", "--id", "1", "--error-code", "Nope").want(t, errs.ExitInvalidInput)
	run(t, "frame", "encode", "--type", "bogus", "--id", "1").want(t, errs.ExitInvalidInput)
	run(t, "frame", "encode", "--type", "call", "--id", strings.Repeat("x", 40), "--action", "Heartbeat").want(t, errs.ExitInvalidInput)
	run(t, "frame", "encode", "--type", "call").want(t, errs.ExitUsage)
}

// writeCerts writes a CA and a certificate it signed, as PEM files.
func writeCerts(t *testing.T) (caFile, certFile string) {
	t.Helper()
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(0x0abc), Subject: pkix.Name{CommonName: "CS001"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour)}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, der []byte) string {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	return write("ca.pem", caDER), write("cs.pem", leafDER)
}

func TestCertHash(t *testing.T) {
	ca, cert := writeCerts(t)
	var res types.CertHashResult
	run(t, "cert", "hash", "--cert", cert, "--issuer", ca, "--format", "json").want(t, errs.ExitOK).decode(t, &res)
	if res.HashAlgorithm != "SHA256" || res.SerialNumber != "abc" || len(res.IssuerNameHash) != 64 || res.OCPP["serialNumber"] != "abc" {
		t.Fatalf("hash: %+v", res)
	}
	run(t, "cert", "hash", "--cert", ca, "--hash", "sha512").want(t, errs.ExitOK).has(t, "SHA512")
	run(t, "cert", "hash", "--cert", ca, "--format", "table").want(t, errs.ExitOK).has(t, "ISSUER_KEY_HASH")
	run(t, "cert", "hash", "--cert", cert).want(t, errs.ExitInvalidInput).errHas(t, "not signed")
	run(t, "cert", "hash", "--cert", cert, "--hash", "md5").want(t, errs.ExitInvalidInput)
	run(t, "cert", "hash", "--cert", "/nonexistent.pem").want(t, errs.ExitInvalidInput)
	bad := filepath.Join(t.TempDir(), "bad.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	run(t, "cert", "hash", "--cert", bad).want(t, errs.ExitInvalidInput)
	run(t, "cert", "hash").want(t, errs.ExitUsage)
	run(t, "cert").want(t, errs.ExitOK).has(t, "hash")
}

func TestRootCommands(t *testing.T) {
	SetBuildMeta("v0.1.0", "v0.1.0", "abc", "2026-10-18")
	run(t, "version").want(t, errs.ExitOK).has(t, "ocppctl version: v0.1.0", "commit:      abc", "build date:", "go version:")
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		if r := run(t, "completion", sh).want(t, errs.ExitOK); len(r.stdout) < 100 {
			t.Fatalf("%s completion: %q", sh, r.stdout)
		}
	}
	run(t, "completion", "tcsh").want(t, errs.ExitUsage)
	run(t, "bogus").want(t, errs.ExitUsage)
	run(t, "actions", "--bogus").want(t, errs.ExitUsage)
	run(t, "actions", "--format", "xml").want(t, errs.ExitInvalidInput)
	run(t, "--help").want(t, errs.ExitOK).has(t, "client", "server", "validate")
	r := run(t, "validate", "Heartbeat", "--payload", "[", "--format", "json").want(t, errs.ExitInvalidInput)
	if kind, code := r.errorCode(t); kind != "invalid_input" || code != "input_decode_failed" {
		t.Fatalf("error %s %s", kind, code)
	}
	t.Setenv("OCPPCTL_COMPRESSION", "maybe")
	run(t, "actions").want(t, errs.ExitInvalidInput).errHas(t, "OCPPCTL_COMPRESSION")
}

func TestRunCompletes(t *testing.T) {
	r := run(t, "__complete", "actions", "--ocpp-version", "")
	if !strings.Contains(r.stdout, "2.0.1") {
		t.Fatalf("completion: %s", r.stdout)
	}
	r = run(t, "__complete", "client", "status", "--status", "Av")
	if !strings.Contains(r.stdout, "Available") {
		t.Fatalf("completion: %s", r.stdout)
	}
}
