// SPDX-License-Identifier: MIT

package csms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"

	"github.com/otfabric/ocppctl/internal/types"
)

// events collects the events of a server.
type events struct {
	mu  sync.Mutex
	got []types.ServerEvent
	ch  chan struct{}
}

func newEvents() *events { return &events{ch: make(chan struct{}, 1)} }

func (e *events) add(ev types.ServerEvent) {
	e.mu.Lock()
	e.got = append(e.got, ev)
	e.mu.Unlock()
	select {
	case e.ch <- struct{}{}:
	default:
	}
}

func (e *events) all() []types.ServerEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]types.ServerEvent(nil), e.got...)
}

// wait waits for an event that match accepts.
func (e *events) wait(t *testing.T, match func(types.ServerEvent) bool) types.ServerEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		for _, ev := range e.all() {
			if match(ev) {
				return ev
			}
		}
		select {
		case <-e.ch:
		case <-deadline:
			t.Fatalf("no such event; got %+v", e.all())
		}
	}
}

func (e *events) waitEvent(t *testing.T, name, identity, action string) types.ServerEvent {
	t.Helper()
	return e.wait(t, func(ev types.ServerEvent) bool {
		return ev.Event == name && (identity == "" || ev.Identity == identity) && (action == "" || ev.Action == action)
	})
}

// running is a server started for a test.
type running struct {
	addr   string
	socket string
	ev     *events
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

// shortDir is a temporary directory with a short path: a Unix socket path is limited to
// about 104 bytes on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// start runs a server with cfg (listening on loopback, with a control socket in a short
// temporary directory unless cfg names one) until the test ends.
func start(t *testing.T, cfg StartConfig) *running {
	t.Helper()
	if cfg.Listen == ":9000" || cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:0"
	}
	if cfg.ControlSocket == DefaultControlSocket {
		cfg.ControlSocket = filepath.Join(shortDir(t), "c.sock")
	}
	r := &running{socket: cfg.ControlSocket, ev: newEvents(), done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	ready := make(chan net.Addr, 1)
	go func() { r.done <- Run(ctx, &cfg, r.ev.add, io.Discard, func(a net.Addr) { ready <- a }) }()
	select {
	case a := <-ready:
		r.addr = a.String()
	case err := <-r.done:
		cancel()
		t.Fatalf("server did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	t.Cleanup(r.stop)
	return r
}

func (r *running) stop() {
	r.once.Do(func() {
		r.cancel()
		select {
		case err := <-r.done:
			_ = err // what Run returns after the cancel is not what these tests check
		case <-time.After(10 * time.Second):
		}
	})
}

func (r *running) url(id string) string { return "ws://" + r.addr + "/" + id }

func plainConfig() StartConfig {
	cfg := NewStartConfig()
	cfg.InsecureTransport = true
	cfg.NoAuth = true
	return cfg
}

// dial connects a station; opts come after the defaults.
func dial(t *testing.T, url string, v ocpp.Version, opts ...client.Option) (*client.Client, error) {
	t.Helper()
	all := append([]client.Option{client.WithVersions(v), client.WithInsecureTransport(), client.WithResponseTimeout(5 * time.Second)}, opts...)
	c, err := client.New(url, all...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, nil
}

func mustDial(t *testing.T, url string, v ocpp.Version, opts ...client.Option) *client.Client {
	t.Helper()
	c, err := dial(t, url, v, opts...)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	return c
}

// call sends a raw CALL and returns the response as a map.
func call(t *testing.T, c *client.Client, action, payload string) (map[string]any, error) {
	t.Helper()
	var raw json.RawMessage
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, action, json.RawMessage(payload), &raw); err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m, nil
}

func mustCall(t *testing.T, c *client.Client, action, payload string) map[string]any {
	t.Helper()
	m, err := call(t, c, action, payload)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return m
}

// pki is a test CA with a server certificate for 127.0.0.1 and a client certificate.
type pki struct {
	dir                           string
	caFile, serverCert, serverKey string
	clientCert, clientKey         string
	pool                          *x509.CertPool
	client                        tls.Certificate
}

func newPKI(t *testing.T, clientCN string) *pki {
	t.Helper()
	p := &pki{dir: t.TempDir()}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	p.pool = x509.NewCertPool()
	p.pool.AddCert(ca)
	p.caFile = p.write(t, "ca.pem", "CERTIFICATE", caDER)
	issue := func(name string, serial int64, tmpl *x509.Certificate) (string, string, tls.Certificate) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl.SerialNumber = big.NewInt(serial)
		tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		cf := p.write(t, name+".pem", "CERTIFICATE", der)
		kf := p.write(t, name+".key", "EC PRIVATE KEY", kb)
		pair, err := tls.LoadX509KeyPair(cf, kf)
		if err != nil {
			t.Fatal(err)
		}
		return cf, kf, pair
	}
	p.serverCert, p.serverKey, _ = issue("server", 2, &x509.Certificate{Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	p.clientCert, p.clientKey, p.client = issue("client", 3, &x509.Certificate{Subject: pkix.Name{CommonName: clientCN},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return p
}

func (p *pki) write(t *testing.T, name, typ string, der []byte) string {
	t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
