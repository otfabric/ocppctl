// SPDX-License-Identifier: MIT

// Package csms is the CSMS behind `ocppctl server`: a go-ocpp server that accepts
// stations, answers them from a model, streams what happens as events, and takes CALLs
// to send to its stations from a control socket.
package csms

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/server"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocppx"
	"github.com/otfabric/ocppctl/internal/types"
)

// ControlSocketEnv names the control socket when --control-socket is not given.
const ControlSocketEnv = "OCPPCTL_CONTROL_SOCKET"

// DefaultControlSocket is the control socket of `server start` and the one `server call`
// and `server stations` connect to: in the current directory, so that the commands find
// each other when run from the same directory.
const DefaultControlSocket = "ocppctl.sock"

// StartConfig configures `server start`.
type StartConfig struct {
	Listen            string   `flag:"listen" env:"OCPPCTL_LISTEN" desc:"Address to listen on, host:port"`
	Path              string   `flag:"path" env:"OCPPCTL_PATH" desc:"URL path prefix; stations connect to <path><identity>"`
	Versions          []string `flag:"ocpp-version" env:"OCPPCTL_OCPP_VERSION" desc:"OCPP versions to accept: 2.1, 2.0.1, 1.6"`
	Config            string   `flag:"config" env:"OCPPCTL_CONFIG" desc:"CSMS model file, YAML or JSON (default: the built-in model, see server generate-config)"`
	PasswordsFile     string   `flag:"passwords-file" env:"OCPPCTL_PASSWORDS_FILE" desc:"File of identity:password lines: HTTP Basic passwords of the stations (security profiles 1 and 2)"`
	NoAuth            bool     `flag:"no-auth" env:"OCPPCTL_NO_AUTH" desc:"Accept every station without authentication (security profile 0)"`
	TLSCert           string   `flag:"tls-cert" env:"OCPPCTL_TLS_CERT" desc:"PEM file of the server certificate: listen with TLS (wss://)"`
	TLSKey            string   `flag:"tls-key" env:"OCPPCTL_TLS_KEY" desc:"PEM file of the server certificate's private key"`
	TLSClientCA       string   `flag:"tls-client-ca" env:"OCPPCTL_TLS_CLIENT_CA" desc:"PEM file of the CAs of station client certificates (security profile 3: identity = certificate CN)"`
	InsecureTransport bool     `flag:"insecure-transport" env:"OCPPCTL_INSECURE_TRANSPORT" desc:"Allow listening without TLS (ws://, security profiles 0 and 1): no encryption"`
	ControlSocket     string   `flag:"control-socket" env:"OCPPCTL_CONTROL_SOCKET" desc:"Unix socket that server call and server stations use; empty disables it"`
	MaxStations       int      `flag:"max-stations" env:"OCPPCTL_MAX_STATIONS" desc:"Most stations connected at once; more are refused with HTTP 503 (0: go-ocpp's default, 10000)"`
	PingInterval      string   `flag:"ping-interval" env:"OCPPCTL_PING_INTERVAL" desc:"Send a WebSocket ping to every station at this interval, and drop it when the pong is late (0: no pings)"`
	RateLimit         float64  `flag:"rate-limit" env:"OCPPCTL_RATE_LIMIT" desc:"Handle at most this many messages a second per station; more wait (0: no limit)"`
	RateBurst         int      `flag:"rate-burst" env:"OCPPCTL_RATE_BURST" desc:"Burst of --rate-limit (default: the rate, rounded up)"`
	Allow             []string `flag:"allow" env:"OCPPCTL_ALLOW" desc:"Accept connections only from these addresses or CIDR prefixes (default: any)"`
	Duration          string   `flag:"duration" env:"OCPPCTL_DURATION" desc:"Stop after this long, such as 10m (default: run until interrupted)"`
	OutputFormat      string   `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format of the events: text, json (JSON Lines) or table"`
	Debug             bool     `flag:"-"`
}

// NewStartConfig returns the defaults.
func NewStartConfig() StartConfig {
	return StartConfig{
		Listen: ":9000", Path: "/", Versions: append([]string(nil), ocppx.DefaultVersions...),
		ControlSocket: DefaultControlSocket, PingInterval: "0", OutputFormat: "text",
	}
}

// plan is a checked StartConfig.
type plan struct {
	model     *Model
	versions  []ocpp.Version
	prefix    string
	tls       *tls.Config
	passwords map[string]string
	certAuth  bool
	allow     []netip.Prefix
	ping      time.Duration
	duration  time.Duration
}

// Check validates the configuration of `server start`, the model included, and reports
// every problem of the model at once.
func Check(cfg *StartConfig) error {
	_, err := cfg.plan()
	return err
}

func invalid(format string, args ...any) error {
	return errs.InvalidInput(errs.CodeInvalidFlagCombination, fmt.Sprintf(format, args...), nil)
}

func (cfg *StartConfig) plan() (*plan, error) {
	p := &plan{}
	var err error
	if p.versions, err = ocppx.ParseVersions(cfg.Versions); err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, err.Error(), err)
	}
	if cfg.Config == "" {
		p.model = Default()
		if err := p.model.Validate(p.versions); err != nil {
			return nil, errs.Internal(err)
		}
	} else if p.model, err = Load(cfg.Config, p.versions); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--config: %v", err), err)
		}
		return nil, errs.InvalidInput(errs.CodeInvalidConfig, fmt.Sprintf("invalid model: %v", err), err)
	}
	p.prefix = "/" + strings.Trim(strings.TrimSpace(cfg.Path), "/") + "/"
	if p.prefix == "//" {
		p.prefix = "/"
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --listen %q: want host:port, such as :9000", cfg.Listen), err)
	}
	if p.tls, err = serverTLS(cfg); err != nil {
		return nil, err
	}
	if p.tls == nil && !cfg.InsecureTransport {
		return nil, invalid("listening without TLS (ws://) exposes everything, passwords included: give --tls-cert and --tls-key, or --insecure-transport on a closed network")
	}
	p.passwords = p.model.Passwords()
	if cfg.PasswordsFile != "" {
		file, err := readPasswords(cfg.PasswordsFile)
		if err != nil {
			return nil, err
		}
		for id, pw := range file {
			p.passwords[id] = pw
		}
	}
	p.certAuth = cfg.TLSClientCA != ""
	if p.certAuth && len(p.passwords) > 0 {
		// Profiles 2 and 3 side by side: a certificate is verified when one is sent.
		p.tls.ClientAuth = tls.VerifyClientCertIfGiven
	}
	switch {
	case cfg.NoAuth && (len(p.passwords) > 0 || p.certAuth):
		return nil, invalid("--no-auth contradicts the passwords and client CA given")
	case !cfg.NoAuth && len(p.passwords) == 0 && !p.certAuth:
		return nil, invalid("no station authentication: give passwords (model stations or --passwords-file), --tls-client-ca, or --no-auth to accept every station")
	}
	if p.allow, err = parseAllow(cfg.Allow); err != nil {
		return nil, err
	}
	if p.ping, err = duration("--ping-interval", cfg.PingInterval); err != nil {
		return nil, err
	}
	if p.duration, err = duration("--duration", cfg.Duration); err != nil {
		return nil, err
	}
	if cfg.MaxStations < 0 || cfg.RateLimit < 0 || cfg.RateBurst < 0 {
		return nil, errs.InvalidInput(errs.CodeInvalidInput, "--max-stations, --rate-limit and --rate-burst must not be negative", nil)
	}
	return p, nil
}

// duration parses an optional duration: empty or 0 is none.
func duration(flag, s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid %s %q: want a duration such as 30s", flag, s), err)
	}
	return d, nil
}

// readPasswords reads identity:password lines; blank lines and # comments are skipped.
func readPasswords(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own file
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--passwords-file: %v", err), err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		id, pw, ok := strings.Cut(line, ":")
		id = strings.TrimSpace(id)
		if !ok || id == "" || pw == "" {
			// The line is not echoed: it may hold a password.
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--passwords-file %s line %d: want identity:password", path, n), nil)
		}
		out[id] = pw
	}
	if err := sc.Err(); err != nil {
		return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--passwords-file: %v", err), err)
	}
	return out, nil
}

func serverTLS(cfg *StartConfig) (*tls.Config, error) {
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return nil, invalid("--tls-cert and --tls-key go together")
	}
	if cfg.TLSCert == "" {
		if cfg.TLSClientCA != "" {
			return nil, invalid("--tls-client-ca needs --tls-cert and --tls-key")
		}
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--tls-cert/--tls-key: %v", err), err)
	}
	out := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	if cfg.TLSClientCA != "" {
		pem, err := os.ReadFile(cfg.TLSClientCA)
		if err != nil {
			return nil, errs.InvalidInput(errs.CodeInputFileOpenFailed, fmt.Sprintf("--tls-client-ca: %v", err), err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errs.InvalidInput(errs.CodeInputDecodeFailed, fmt.Sprintf("--tls-client-ca %s: no PEM certificate in it", cfg.TLSClientCA), nil)
		}
		out.ClientCAs = pool
		out.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return out, nil
}

func parseAllow(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, errs.InvalidInput(errs.CodeInvalidInput, fmt.Sprintf("invalid --allow %q: use an address or a CIDR prefix", s), err)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// CSMS is a running `server start`.
type CSMS struct {
	plan *plan
	srv  *server.Server
	emit func(types.ServerEvent)

	// pending holds the extensions negotiated by handshakes, by remote address, until the
	// session of the connection starts; sessions holds what is known of running sessions.
	mu       sync.Mutex
	pending  map[string]string
	sessions map[*server.Session]bool // compression

	callTimeout time.Duration
}

// Run serves stations as cfg says until ctx ends or the duration of cfg has passed. Every
// event goes to emit (from several goroutines, one at a time); with debug, frames and the
// library's log go to stderr. ready, when not nil, is called once listening.
func Run(ctx context.Context, cfg *StartConfig, emit func(types.ServerEvent), stderr io.Writer, ready func(addr net.Addr)) error {
	p, err := cfg.plan()
	if err != nil {
		return err
	}
	var emitMu sync.Mutex
	c := &CSMS{plan: p, pending: map[string]string{}, sessions: map[*server.Session]bool{}, callTimeout: 30 * time.Second,
		emit: func(e types.ServerEvent) {
			emitMu.Lock()
			defer emitMu.Unlock()
			emit(e)
		}}
	if c.srv, err = server.New(c.options(cfg, stderr)...); err != nil {
		return errs.InvalidInput(errs.CodeInvalidConfig, err.Error(), err)
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return errs.New(errs.KindTransport, errs.CodeListenFailed, fmt.Sprintf("cannot listen on %s: %v", cfg.Listen, err), err)
	}
	if p.tls != nil {
		ln = tls.NewListener(ln, p.tls)
	}
	var ctl *control
	if cfg.ControlSocket != "" {
		if ctl, err = listenControl(cfg.ControlSocket, c); err != nil {
			_ = ln.Close()
			return err
		}
	}
	if p.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.duration)
		defer cancel()
	}

	hs := &http.Server{Handler: c, ReadHeaderTimeout: server.DefaultHandshakeTimeout, ErrorLog: newErrorLog(cfg.Debug, stderr)}
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()
	if ctl != nil {
		go ctl.serve(ctx)
	}

	ev := types.NewServerEvent("listening")
	ev.Address = ln.Addr().String()
	for _, v := range p.versions {
		ev.Versions = append(ev.Versions, v.String())
	}
	if ctl != nil {
		ev.ControlSocket = cfg.ControlSocket
	}
	c.emit(ev)
	if ready != nil {
		ready(ln.Addr())
	}

	var serveErr error
	select {
	case serveErr = <-served:
	case <-ctx.Done():
	}
	c.emit(types.NewServerEvent("stopping"))
	if ctl != nil {
		ctl.close()
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.srv.Shutdown(sctx)
	_ = hs.Shutdown(sctx)
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return errs.New(errs.KindTransport, errs.CodeListenFailed, fmt.Sprintf("server stopped: %v", serveErr), serveErr)
	}
	return nil
}

// newErrorLog is the log of the HTTP server (failed TLS handshakes, for one): shown with
// --debug only.
func newErrorLog(debug bool, stderr io.Writer) *log.Logger {
	if debug && stderr != nil {
		return log.New(stderr, "http: ", log.LstdFlags)
	}
	return log.New(io.Discard, "", 0)
}

func (c *CSMS) options(cfg *StartConfig, stderr io.Writer) []server.Option {
	p := c.plan
	a := newAnswerer(p.model, c.emit)
	var opts []server.Option
	for _, v := range p.versions {
		opts = append(opts, server.WithHandler(v, dispatcher{a: a, v: v}))
	}
	if cfg.NoAuth {
		opts = append(opts, server.WithoutAuthentication())
	} else {
		var auths []server.Authenticator
		if p.certAuth {
			auths = append(auths, server.CertificateAuth())
		}
		if len(p.passwords) > 0 {
			pw := p.passwords
			auths = append(auths, server.BasicAuth(func(_ context.Context, id string) (string, bool) {
				s, ok := pw[id]
				return s, ok
			}))
		}
		opts = append(opts, server.WithAuthenticator(c.reporting(server.AnyOf(auths...))))
	}
	if cfg.MaxStations > 0 {
		opts = append(opts, server.WithMaxStations(cfg.MaxStations))
	}
	if p.ping > 0 {
		opts = append(opts, server.WithPingInterval(p.ping))
	}
	if cfg.RateLimit > 0 {
		burst := cfg.RateBurst
		if burst == 0 {
			burst = int(cfg.RateLimit)
			if float64(burst) < cfg.RateLimit {
				burst++
			}
		}
		opts = append(opts, server.WithIncomingRateLimit(cfg.RateLimit, burst))
	}
	// CALLs from the control socket carry their own deadline.
	opts = append(opts, server.WithResponseTimeout(time.Hour))
	opts = append(opts, server.WithSessionHandler(c.connected, c.disconnected))
	if cfg.Debug && stderr != nil {
		opts = append(opts,
			server.WithLogger(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))),
			server.WithFrameObserver(conn.Trace(stderr)))
	}
	return opts
}

// reporting wraps an authenticator to report refusals as events. Credentials are never
// reported.
func (c *CSMS) reporting(a server.Authenticator) server.Authenticator {
	return server.AuthenticatorFunc(func(ctx context.Context, r server.ConnectRequest) error {
		err := a.Authenticate(ctx, r)
		if err != nil {
			ev := types.NewServerEvent("refused")
			ev.Identity, ev.Remote = r.StationID, r.RemoteAddr
			switch {
			case errors.Is(err, server.ErrUnknownStation):
				ev.Reason = "unknown station identity (HTTP 404)"
			case !r.HasBasicAuth && (r.TLS == nil || len(r.TLS.PeerCertificates) == 0):
				ev.Reason = "no credentials (HTTP 401)"
			default:
				ev.Reason = "authentication failed (HTTP 401)"
			}
			c.emit(ev)
		}
		return err
	})
}

// ServeHTTP checks the path and the remote address, records the negotiated extensions and
// hands the connection to the go-ocpp server.
func (c *CSMS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), c.plan.prefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		ev := types.NewServerEvent("refused")
		ev.Remote, ev.Reason = r.RemoteAddr, "path "+r.URL.EscapedPath()+" is not "+c.plan.prefix+"<identity> (HTTP 404)"
		c.emit(ev)
		http.Error(w, "not found: connect to "+c.plan.prefix+"<identity>", http.StatusNotFound)
		return
	}
	if len(c.plan.allow) > 0 && !allowed(c.plan.allow, r.RemoteAddr) {
		ev := types.NewServerEvent("refused")
		ev.Remote, ev.Reason = r.RemoteAddr, "address not allowed (HTTP 403)"
		c.emit(ev)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rw := &recordingWriter{ResponseWriter: w, onUpgrade: func(h http.Header) {
		c.mu.Lock()
		c.pending[r.RemoteAddr] = h.Get("Sec-WebSocket-Extensions")
		c.mu.Unlock()
	}}
	defer func() {
		c.mu.Lock()
		delete(c.pending, r.RemoteAddr)
		c.mu.Unlock()
	}()
	c.srv.ServeHTTP(rw, r)
}

func allowed(list []netip.Prefix, remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range list {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (c *CSMS) connected(s *server.Session) {
	c.mu.Lock()
	compressed := strings.Contains(c.pending[s.RemoteAddr()], "permessage-deflate")
	c.sessions[s] = compressed
	c.mu.Unlock()
	ev := types.NewServerEvent("connected")
	ev.Identity, ev.Version, ev.Remote, ev.SessionID = s.ID(), s.Version().String(), s.RemoteAddr(), s.SessionID()
	ev.TLS, ev.Compression = ptr(s.TLS() != nil), &compressed
	c.emit(ev)
}

func (c *CSMS) disconnected(s *server.Session, err error) {
	c.mu.Lock()
	delete(c.sessions, s)
	c.mu.Unlock()
	ev := types.NewServerEvent("disconnected")
	ev.Identity, ev.Version, ev.Remote, ev.SessionID = s.ID(), s.Version().String(), s.RemoteAddr(), s.SessionID()
	switch {
	case err == nil:
		ev.Reason = "closed by the server"
	case errors.Is(err, server.ErrReplaced):
		ev.Reason = "replaced by a new connection of the station"
	default:
		ev.Reason = err.Error()
	}
	c.emit(ev)
}

// Stations lists the connected stations.
func (c *CSMS) Stations() []types.StationInfo {
	out := []types.StationInfo{}
	for _, s := range c.srv.Stations() {
		c.mu.Lock()
		compressed := c.sessions[s]
		c.mu.Unlock()
		out = append(out, types.StationInfo{Identity: s.ID(), Version: s.Version().String(), Remote: s.RemoteAddr(),
			ConnectedAt: types.Millis(s.ConnectedAt()), TLS: s.TLS() != nil, Compression: compressed, SessionID: s.SessionID()})
	}
	return out
}

// recordingWriter passes the handshake through and reports the response headers of a
// WebSocket upgrade.
type recordingWriter struct {
	http.ResponseWriter
	onUpgrade func(http.Header)
}

func (w *recordingWriter) WriteHeader(code int) {
	if code == http.StatusSwitchingProtocols && w.onUpgrade != nil {
		w.onUpgrade(w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker for the WebSocket upgrade.
func (w *recordingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
