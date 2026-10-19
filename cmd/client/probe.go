// SPDX-License-Identifier: MIT

package client

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/clientops"
	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/runner"
)

func newProbeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Connect to a CSMS and report what the handshake negotiated",
		Long: `Connect, report what the WebSocket handshake negotiated, and disconnect without
sending an OCPP message: the OCPP version the CSMS chose, TLS version and certificate,
the authentication used, whether compression was negotiated, and the handshake time.

With --each-version ocppctl connects once per version offered, each time offering only
that one, and lists every version the CSMS accepts. The command fails only when no
attempt is accepted; a refused handshake reports its HTTP status and what it usually
means (401: wrong password, 404: unknown identity, ...).`,
		Example: `
  # Which version does the CSMS pick?
  ocppctl client probe --url wss://csms.example.com/ocpp/CS001 --password-file pw.txt

  # Every version it accepts
  ocppctl client probe --url wss://csms.example.com/ocpp/CS001 --each-version --format table

  # Security profile 3 against a private CA
  ocppctl client probe --url wss://csms.test:8443/ocpp/CS001 \
    --tls-ca ca.pem --tls-cert cs001.pem --tls-key cs001.key

  # A local CSMS without TLS
  ocppctl client probe --url ws://localhost:9000/CS001 --insecure-transport
`,
		Args: cobra.NoArgs,
	}
	cfg := clientops.ProbeConfig{Target: conn.NewTarget(), OutputFormat: "text"}
	wire(cmd, &cfg)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg.Debug = cli.Debug(cmd)
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(ctx context.Context) (any, error) { return clientops.Probe(ctx, &cfg, cmd.ErrOrStderr()) })
	}
	return cmd
}
