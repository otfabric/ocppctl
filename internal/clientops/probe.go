// SPDX-License-Identifier: MIT

package clientops

import (
	"context"
	"crypto/tls"
	"io"

	"github.com/otfabric/go-ocpp"

	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/ocpperr"
	"github.com/otfabric/ocppctl/internal/types"
)

// ProbeConfig configures `client probe`.
type ProbeConfig struct {
	conn.Target
	EachVersion  bool   `flag:"each-version" desc:"Connect once per offered version, to learn every version the CSMS accepts"`
	OutputFormat string `flag:"format" env:"OCPPCTL_FORMAT" desc:"Output format: text, json or table"`
	Debug        bool   `flag:"-"`
}

// Probe connects, reports what the handshake negotiated, and disconnects without sending
// a message. It fails (with the error of the first attempt) only when no attempt was
// accepted.
func Probe(ctx context.Context, cfg *ProbeConfig, stderr io.Writer) (*types.ProbeResult, error) {
	p, err := cfg.Parse()
	if err != nil {
		return nil, err
	}
	offers := [][]ocpp.Version{p.Versions}
	if cfg.EachVersion {
		offers = nil
		for _, v := range p.Versions {
			offers = append(offers, []ocpp.Version{v})
		}
	}
	res := &types.ProbeResult{Target: p.URL, Identity: p.Identity, Attempts: []types.ProbeAttempt{}, Accepted: []string{}}
	var first error
	for _, offer := range offers {
		q := *p
		q.Versions = offer
		a, err := probeOnce(ctx, &q, cfg.Debug, stderr)
		if ctx.Err() != nil {
			return nil, ocpperr.Wrap(ctx.Err(), "", false)
		}
		res.Attempts = append(res.Attempts, a)
		if a.OK {
			res.Accepted = append(res.Accepted, a.Negotiated)
		} else if first == nil {
			first = err
		}
	}
	if len(res.Accepted) == 0 {
		return nil, first
	}
	return res, nil
}

func probeOnce(ctx context.Context, p *conn.Parsed, debug bool, stderr io.Writer) (types.ProbeAttempt, error) {
	a := types.ProbeAttempt{Auth: auth(p)}
	for _, v := range p.Versions {
		a.Offered = append(a.Offered, v.String())
	}
	c, err := conn.Dial(ctx, p, conn.Options{Debug: debug, Stderr: stderr})
	if err != nil {
		if e, ok := errs.As(err); ok {
			a.Error = &types.ErrorInfo{Kind: string(e.Kind), Code: e.Code, Message: e.Message}
			if d, ok := e.OCPP.(ocpperr.HandshakeDetail); ok {
				a.HTTPStatus = d.HTTPStatus
			}
		}
		return a, err
	}
	defer func() { _ = c.Close() }()
	h := c.Handshake
	a.OK, a.Negotiated, a.HTTPStatus = true, c.Version().String(), h.Status
	a.Compression, a.HandshakeMS = h.Compressed(), h.Duration.Milliseconds()
	a.TLS = tlsInfo(h.TLS, p.TLS)
	return a, nil
}

func auth(p *conn.Parsed) string {
	basic := p.Password != ""
	cert := p.TLS != nil && len(p.TLS.Certificates) > 0
	switch {
	case basic && cert:
		return "basic+certificate"
	case basic:
		return "basic"
	case cert:
		return "certificate"
	}
	return "none"
}

func tlsInfo(st *tls.ConnectionState, cfg *tls.Config) *types.TLSInfo {
	if st == nil {
		return nil
	}
	i := &types.TLSInfo{Version: tls.VersionName(st.Version), CipherSuite: tls.CipherSuiteName(st.CipherSuite), ServerName: st.ServerName}
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		i.Subject, i.Issuer, i.NotAfter = leaf.Subject.String(), leaf.Issuer.String(), leaf.NotAfter
	}
	i.ClientCert = cfg != nil && len(cfg.Certificates) > 0
	return i
}
