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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/client"

	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/format"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/readlimit"
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
	Headers           []string `flag:"header" desc:"Extra handshake header NAME=VALUE (repeatable); a value on the command line is visible to other local users, so give tokens with --header-file"`
	HeaderFile        string   `flag:"header-file" env:"OCPPCTL_HEADER_FILE" desc:"File of NAME=VALUE lines: extra handshake headers, such as a token, kept off the command line"`
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
	if err == nil && u.User != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidURL, "credentials do not go in the URL: use --password-file or OCPPCTL_PASSWORD", nil)
	}
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		// The URL may hold a password (it did not parse, or is not ws[s]): neither the
		// message nor the cause repeats it. url.Error.Err is the reason without the URL.
		var cause error
		if ue, ok := errors.AsType[*url.Error](err); ok {
			cause = ue.Err
		}
		return nil, errs.InvalidInput(errs.CodeInvalidURL, fmt.Sprintf("invalid --url %q: want ws[s]://host[:port]/path/<identity>", redact(t.URL)), cause)
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
	if t.HeaderFile != "" {
		if p.Headers, err = headerFile(t.HeaderFile); err != nil {
			return nil, err
		}
	}
	for _, h := range t.Headers {
		name, value, ok := strings.Cut(h, "=")
		if !ok || strings.TrimSpace(name) == "" {
			// A header may carry a token: the error names the header, never its value.
			name, _, _ = strings.Cut(name, ":")
			return nil, errs.InvalidInput(errs.CodeInvalidInput,
				fmt.Sprintf("invalid --header %q (value not shown): want NAME=VALUE", strings.TrimSpace(name)), nil)
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

// redact replaces the user information of a URL that may not parse ("user:password@" in
// its authority) with "REDACTED@".
func redact(raw string) string {
	start := 0
	if i := strings.Index(raw, "://"); i >= 0 {
		start = i + 3
	}
	end := len(raw)
	if i := strings.IndexAny(raw[start:], "/?#"); i >= 0 {
		end = start + i
	}
	at := strings.LastIndex(raw[start:end], "@")
	if at < 0 {
		return raw
	}
	return raw[:start] + "REDACTED" + raw[start+at:]
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
		b, err := readSecret("--password-file", file)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return os.Getenv(PasswordEnv), nil
}

// readSecret reads a file of flag that holds a secret, at most readlimit.Secret bytes. No
// error repeats its content.
func readSecret(flag, file string) ([]byte, error) {
	b, err := readlimit.File(file, readlimit.Secret)
	if tl, ok := errors.AsType[*readlimit.TooLargeError](err); ok {
		return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("%s %s: %v", flag, file, tl), err)
	}
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("%s: %v", flag, err), err)
	}
	return b, nil
}

// headerFile reads the NAME=VALUE lines of --header-file; blank lines and # comments are
// skipped, and a UTF-8 byte order mark, as some editors write, is not part of the first
// name. A malformed line is reported by its number, never by its content: it may hold a
// token.
func headerFile(file string) ([][2]string, error) {
	b, err := readSecret("--header-file", file)
	if err != nil {
		return nil, err
	}
	var out [][2]string
	n := 0
	for line := range strings.SplitSeq(strings.TrimPrefix(string(b), "\uFEFF"), "\n") {
		n++
		line = strings.TrimRight(line, "\r")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed,
				fmt.Sprintf("--header-file %s line %d: want NAME=VALUE (the line is not shown)", file, n), nil)
		}
		out = append(out, [2]string{strings.TrimSpace(name), value})
	}
	return out, nil
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

// Handshake is what the WebSocket handshake negotiated.
type Handshake struct {
	Status      int
	Subprotocol string
	Compression bool
	TLS         *tls.ConnectionState
	Duration    time.Duration
}

// Compressed reports whether permessage-deflate was negotiated.
func (h *Handshake) Compressed() bool { return h.Compression }

// recorder records the handshake without wrapping the HTTP client's transport: go-ocpp
// bounds the closing handshake of a connection, and keeps Dial within its context, only
// when that transport is an *http.Transport. The TLS state and the duration come from an
// httptrace.ClientTrace on the context of Connect, the compression from go-ocpp's
// Metrics.OnConnect, the subprotocol from the version the client negotiated.
type recorder struct {
	ocpp.NopMetrics
	mu          sync.Mutex
	start       time.Time
	took        time.Duration
	tls         *tls.ConnectionState
	compression bool
}

// OnConnect implements ocpp.Metrics: it is called before Connect returns.
func (r *recorder) OnConnect(info ocpp.ConnInfo) {
	r.mu.Lock()
	r.compression = info.Compression
	r.mu.Unlock()
}

// trace returns ctx with a trace of the handshake requests; the last request counts, as
// for a redirect.
func (r *recorder) trace(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(string) {
			r.mu.Lock()
			r.start, r.took, r.tls = time.Now(), 0, nil
			r.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			tc, ok := info.Conn.(*tls.Conn)
			if !ok {
				return
			}
			st := tc.ConnectionState()
			r.mu.Lock()
			r.tls = &st
			r.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			r.mu.Lock()
			r.took = time.Since(r.start)
			r.mu.Unlock()
		},
	})
}

func (r *recorder) handshake(v ocpp.Version) Handshake {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Handshake{Status: http.StatusSwitchingProtocols, Subprotocol: v.Subprotocol(), Compression: r.compression,
		TLS: r.tls, Duration: r.took}
}

// httpClient is the HTTP client of the handshake: a plain *http.Transport, so that go-ocpp
// can bound the connection's closing handshake.
func httpClient(p *Parsed) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: p.TLS, Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: false}}
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
	rec := &recorder{}
	opts := []client.Option{
		client.WithVersions(p.Versions...),
		client.WithHTTPClient(httpClient(p)),
		client.WithHandshakeTimeout(p.ConnectTimeout),
		client.WithResponseTimeout(p.Timeout),
		client.WithMetrics(rec),
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
	cctx, cancel := context.WithTimeout(c.rec.trace(ctx), p.ConnectTimeout)
	defer cancel()
	if err := c.Connect(cctx); err != nil {
		_ = c.Close()
		if ctx.Err() != nil {
			return nil, ocpperr.Wrap(ctx.Err(), "", false)
		}
		return nil, ocpperr.Wrap(err, "", false)
	}
	c.Handshake = c.rec.handshake(c.Version())
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
		// The frame is the peer's, as it came: escaped, it cannot drive the terminal or
		// start a trace line of its own.
		_, _ = fmt.Fprintf(w, "%s %s %s %s\n", time.Now().UTC().Format("15:04:05.000"),
			format.EscapeLine(info.StationID), arrow, format.EscapeLine(string(frame)))
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
		b, err = readlimit.Read(stdin, readlimit.Payload)
	case strings.HasPrefix(arg, "@"):
		b, err = readlimit.File(arg[1:], readlimit.Payload)
	default:
		b = []byte(arg)
	}
	if tl, ok := errors.AsType[*readlimit.TooLargeError](err); ok {
		// Read from a file or stdin, a payload is at most what an OCPP-J frame carries:
		// reading stops there, so that an endless stdin does not fill memory.
		return nil, errs.InvalidInput(errs.CodeInvalidPayload, fmt.Sprintf("--payload: %v, more than an OCPP-J frame carries", tl), err)
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
