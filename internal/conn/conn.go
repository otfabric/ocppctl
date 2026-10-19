// SPDX-License-Identifier: MIT

// Package conn connects ocppctl to a CSMS as a Charging Station: the flags every client
// command shares (URL, versions, credentials, TLS, compression, timeouts) and Dial, which
// builds a go-ocpp client from them and records what the handshake negotiated.
package conn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
)

// PasswordEnv holds the HTTP Basic password when --password-file is not given. It is read
// when a command runs, never as a flag default, so --help never shows it.
const PasswordEnv = "OCPPCTL_PASSWORD"

// Target is the flags of a connection to a CSMS. Embed it in a command's configuration.
type Target struct {
	URL               string   `flag:"url" env:"OCPPCTL_URL" desc:"CSMS endpoint followed by the station identity: ws[s]://host[:port]/path/<identity>"`
	Versions          []string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP versions to offer, in order of preference: 1.6, 2.0.1, 2.1"`
	PasswordFile      string   `flag:"password-file" env:"OCPPCTL_PASSWORD_FILE" desc:"File holding the HTTP Basic password (security profiles 1 and 2); or set OCPPCTL_PASSWORD"`
	TLSCA             string   `flag:"tls-ca" env:"OCPPCTL_TLS_CA" desc:"PEM file of the CA certificates that verify the CSMS (default: the system roots)"`
	TLSCert           string   `flag:"tls-cert" env:"OCPPCTL_TLS_CERT" desc:"PEM file of the station's client certificate (security profile 3)"`
	TLSKey            string   `flag:"tls-key" env:"OCPPCTL_TLS_KEY" desc:"PEM file of the client certificate's private key"`
	TLSServerName     string   `flag:"tls-server-name" env:"OCPPCTL_TLS_SERVER_NAME" desc:"Name to verify the CSMS certificate against (default: the URL host)"`
	InsecureTransport bool     `flag:"insecure-transport" env:"OCPPCTL_INSECURE_TRANSPORT" desc:"Allow ws:// (security profiles 0 and 1): no encryption"`
	Compression       bool     `flag:"compression" env:"OCPPCTL_COMPRESSION" desc:"Offer permessage-deflate compression"`
	Headers           []string `flag:"header" desc:"Extra handshake header NAME=VALUE (repeatable)"`
	Timeout           string   `flag:"timeout" env:"OCPPCTL_TIMEOUT" desc:"Response timeout of a CALL"`
	ConnectTimeout    string   `flag:"connect-timeout" env:"OCPPCTL_CONNECT_TIMEOUT" desc:"Dial, TLS and WebSocket handshake timeout"`
}

// NewTarget returns a Target with its defaults, before the environment and flags apply.
func NewTarget() Target {
	return Target{Versions: append([]string(nil), ocppx.DefaultVersions...), Timeout: "30s", ConnectTimeout: "10s"}
}

// Parsed is a validated Target.
type Parsed struct {
	URL            string
	Identity       string
	Versions       []ocpp.Version
	Password       string
	TLS            *tls.Config // nil for ws://
	Headers        [][2]string
	Timeout        time.Duration
	ConnectTimeout time.Duration
	Compression    bool
	Insecure       bool
}

// Parse validates t: everything that can be checked before connecting is, so that a bad
// invocation sends nothing (exit 3).
func (t Target) Parse() (*Parsed, error) {
	if strings.TrimSpace(t.URL) == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, "--url is required (or OCPPCTL_URL)", nil)
	}
	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, fmt.Sprintf("invalid --url %q: want ws[s]://host[:port]/path/<identity>", t.URL), err)
	}
	if u.User != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, "credentials do not go in the URL: use --password-file or OCPPCTL_PASSWORD", nil)
	}
	p := &Parsed{URL: t.URL, Compression: t.Compression, Insecure: t.InsecureTransport}
	seg := u.EscapedPath()
	seg = seg[strings.LastIndex(seg, "/")+1:]
	if p.Identity, err = url.PathUnescape(seg); err != nil || p.Identity == "" {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, fmt.Sprintf("invalid --url %q: it must end with the station identity", t.URL), err)
	}
	if p.Versions, err = ocppx.ParseVersions(t.Versions); err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	if p.Timeout, err = duration("--timeout", t.Timeout); err != nil {
		return nil, err
	}
	if p.ConnectTimeout, err = duration("--connect-timeout", t.ConnectTimeout); err != nil {
		return nil, err
	}
	if p.Password, err = password(t.PasswordFile); err != nil {
		return nil, err
	}
	for _, h := range t.Headers {
		name, value, ok := strings.Cut(h, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --header %q: want NAME=VALUE", h), nil)
		}
		p.Headers = append(p.Headers, [2]string{strings.TrimSpace(name), value})
	}
	tlsFlags := t.TLSCA != "" || t.TLSCert != "" || t.TLSKey != "" || t.TLSServerName != ""
	switch u.Scheme {
	case "ws":
		if tlsFlags {
			return nil, errs.InvalidInput(errs.CodeInvalidFlagCombination, "TLS flags need a wss:// URL", nil)
		}
		if !t.InsecureTransport {
			return nil, errs.InvalidInput(errs.CodeInvalidFlagCombination,
				"ws:// sends everything, the password included, in clear text: use wss://, or --insecure-transport on a closed network", nil)
		}
	case "wss":
		if p.TLS, err = tlsConfig(t); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func duration(flag, s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d <= 0 {
		return 0, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid %s %q: want a positive duration such as 30s", flag, s), err)
	}
	return d, nil
}

func password(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--password-file: %v", err), err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return os.Getenv(PasswordEnv), nil
}

func tlsConfig(t Target) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.TLSServerName}
	if t.TLSCA != "" {
		pem, err := os.ReadFile(t.TLSCA)
		if err != nil {
			return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--tls-ca: %v", err), err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--tls-ca %s: no PEM certificate in it", t.TLSCA), nil)
		}
		cfg.RootCAs = pool
	}
	if (t.TLSCert == "") != (t.TLSKey == "") {
		return nil, errs.InvalidInput(errs.CodeInvalidFlagCombination, "--tls-cert and --tls-key go together", nil)
	}
	if t.TLSCert != "" {
		pair, err := tls.LoadX509KeyPair(t.TLSCert, t.TLSKey)
		if err != nil {
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--tls-cert/--tls-key: %v", err), err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// Handshake is what the WebSocket handshake negotiated, as the HTTP response showed it.
type Handshake struct {
	Status      int
	Subprotocol string
	Extensions  string
	TLS         *tls.ConnectionState
	Duration    time.Duration
}

// Compressed reports whether permessage-deflate was negotiated.
func (h *Handshake) Compressed() bool { return strings.Contains(h.Extensions, "permessage-deflate") }

// recorder is the HTTP client's transport: it records the handshake response.
type recorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	last Handshake
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := r.base.RoundTrip(req)
	if resp != nil {
		r.mu.Lock()
		r.last = Handshake{Status: resp.StatusCode, Subprotocol: resp.Header.Get("Sec-WebSocket-Protocol"),
			Extensions: resp.Header.Get("Sec-WebSocket-Extensions"), TLS: resp.TLS, Duration: time.Since(start)}
		r.mu.Unlock()
	}
	return resp, err
}

func (r *recorder) handshake() Handshake {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// Options are what a command adds to a connection.
type Options struct {
	// Handler answers the CSMS's CALLs on the negotiated version; nil answers NotImplemented.
	Handler func(ocpp.Version) ocpp.Dispatcher
	// Debug traces every frame and the library's log to Stderr.
	Debug  bool
	Stderr io.Writer
	// Extra client options (reconnect, state handler, interceptors).
	Extra []client.Option
}

// Conn is an open connection to a CSMS.
type Conn struct {
	*client.Client
	Parsed    *Parsed
	Handshake Handshake
	rec       *recorder
}

// New builds the client without connecting.
func New(p *Parsed, o Options) (*Conn, error) {
	rec := &recorder{base: &http.Transport{TLSClientConfig: p.TLS, Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: false}}
	opts := []client.Option{
		client.WithVersions(p.Versions...),
		client.WithHTTPClient(&http.Client{Transport: rec}),
		client.WithHandshakeTimeout(p.ConnectTimeout),
		client.WithResponseTimeout(p.Timeout),
	}
	if p.Insecure {
		opts = append(opts, client.WithInsecureTransport())
	}
	if p.Password != "" {
		opts = append(opts, client.WithBasicAuth(p.Identity, p.Password))
	}
	if p.Compression {
		opts = append(opts, client.WithCompression())
	}
	for _, h := range p.Headers {
		opts = append(opts, client.WithHeader(h[0], h[1]))
	}
	if o.Handler != nil {
		for _, v := range p.Versions {
			opts = append(opts, client.WithHandler(v, o.Handler(v)))
		}
	}
	if o.Debug && o.Stderr != nil {
		opts = append(opts,
			client.WithLogger(slog.New(slog.NewTextHandler(o.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))),
			client.WithFrameObserver(Trace(o.Stderr)))
	}
	opts = append(opts, o.Extra...)
	c, err := client.New(p.URL, opts...)
	if err != nil {
		return nil, ocpperr.Wrap(err, "", false)
	}
	return &Conn{Client: c, Parsed: p, rec: rec}, nil
}

// Dial builds the client and connects once.
func Dial(ctx context.Context, p *Parsed, o Options) (*Conn, error) {
	c, err := New(p, o)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, p.ConnectTimeout)
	defer cancel()
	if err := c.Connect(cctx); err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, ocpperr.Wrap(ctx.Err(), "", false)
		}
		return nil, ocpperr.Wrap(err, "", false)
	}
	c.Handshake = c.rec.handshake()
	return c, nil
}

// ShutdownTimeout bounds a graceful close: the answers and SENDs in flight are
// written, then the connection is closed.
const ShutdownTimeout = 5 * time.Second

// Stop closes the connection gracefully (go-ocpp's Shutdown): what is in
// flight is written first, at most for ShutdownTimeout. It does not depend on
// a context the user may have cancelled already (Ctrl-C).
func (c *Conn) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	return c.Shutdown(ctx)
}

// Trace returns a frame observer that writes every frame to w, ">" for sent and "<" for
// received.
func Trace(w io.Writer) ocpp.FrameObserver {
	var mu sync.Mutex
	return func(info ocpp.ConnInfo, dir ocpp.Direction, frame []byte) {
		arrow := "<"
		if dir == ocpp.Outgoing {
			arrow = ">"
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(w, "%s %s %s %s\n", time.Now().UTC().Format("15:04:05.000"), info.StationID, arrow, frame)
	}
}

// ReadPayload reads a payload argument: inline JSON, @file, or - for stdin. Empty is {}.
func ReadPayload(arg string, stdin io.Reader) (json.RawMessage, error) {
	var b []byte
	var err error
	switch {
	case arg == "":
		return json.RawMessage(`{}`), nil
	case arg == "-":
		b, err = io.ReadAll(stdin)
	case strings.HasPrefix(arg, "@"):
		b, err = os.ReadFile(arg[1:])
	default:
		b = []byte(arg)
	}
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--payload: %v", err), err)
	}
	var probe any
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--payload is not JSON: %v", err), err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, errs.InvalidInput(errs.CodeInvalidPayload, "--payload must be a JSON object", nil)
	}
	return json.RawMessage(b), nil
}
