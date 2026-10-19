// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"sort"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/offline"
	"github.com/otfabric/ocppctl/internal/runner"
	"github.com/otfabric/ocppctl/internal/types"
)

var versionValues = []string{"1.6", "2.0.1", "2.1"}

func versionCompletion(cmd *cobra.Command) {
	_ = cli.RegisterEnumFlagCompletion(cmd, "ocpp-version", versionValues)
}

func newActionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "List the actions of an OCPP version, with who sends them",
		Long: `List every action of an OCPP version: its sender (station, csms, or both for
DataTransfer) and its kind (call, or send for the OCPP 2.1 messages that are never
answered). Needs no connection.`,
		Example: `
  # Every OCPP 2.0.1 action, as a table
  ocppctl actions --ocpp-version 2.0.1 --format table

  # What a CSMS can send a 1.6 station
  ocppctl actions --ocpp-version 1.6 --from csms

  # The SENDs of OCPP 2.1
  ocppctl actions --ocpp-version 2.1 --format json | jq -r '.actions[] | select(.kind=="send") | .name'
`,
		Args: cobra.NoArgs,
	}
	cfg := offline.ActionsConfig{Version: "2.0.1", OutputFormat: "text"}
	runner.RegisterCommandCfg(cmd, &cfg, versionCompletion, func(c *cobra.Command) {
		_ = cli.RegisterEnumFlagCompletion(c, "from", []string{"station", "csms"})
	})
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(context.Context) (any, error) { return offline.Actions(&cfg) })
	}
	return cmd
}

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <Action>",
		Short: "Validate a request or response payload against the OCA schema",
		Long: `Validate a payload against the JSON schema the Open Charge Alliance publishes for
the action, as go-ocpp does on every message it sends and receives.

A valid payload exits 0. An invalid one prints the violation (the field, the rule, and
the CALLERROR code a peer would answer with) and exits 3.`,
		Example: `
  # Check a request before sending it
  ocppctl validate BootNotification --ocpp-version 2.0.1 \
    --payload '{"reason":"PowerUp","chargingStation":{"vendorName":"OT Fabric","model":"X"}}'

  # Check a response a CSMS sent, from a file
  ocppctl validate BootNotification --ocpp-version 1.6 --response --payload @boot-response.json

  # In a pipeline
  ocppctl client boot --ocpp-version 2.1 --payload-only | ocppctl validate BootNotification --ocpp-version 2.1 --payload -
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := offline.ValidateConfig{Version: "2.0.1", OutputFormat: "text"}
	runner.RegisterCommandCfg(cmd, &cfg, versionCompletion)
	cli.MustMarkFlagRequired(cmd, "payload")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Action = args[0]
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(context.Context) (any, error) { return offline.Validate(&cfg, cmd.InOrStdin()) },
			runner.WithSuccessExit(func(v any) int {
				if r, ok := v.(*types.ValidateResult); ok && !r.Valid {
					return errs.ExitInvalidInput
				}
				return errs.ExitOK
			}))
	}
	return cmd
}

func newFrameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frame",
		Short: "Decode and encode OCPP-J frames",
		Long: `The OCPP-J RPC framing: the JSON arrays that carry OCPP messages over the WebSocket.

  CALL             [2, "<id>", "<action>", {payload}]
  CALLRESULT       [3, "<id>", {payload}]
  CALLERROR        [4, "<id>", "<code>", "<description>", {details}]
  CALLRESULTERROR  [5, "<id>", "<code>", "<description>", {details}]  OCPP 2.1 only
  SEND             [6, "<id>", "<action>", {payload}]                 OCPP 2.1 only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newFrameDecodeCmd(), newFrameEncodeCmd())
	return cmd
}

func newFrameDecodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decode <frame|->",
		Short: "Decode an OCPP-J frame, and validate the payload of a CALL or SEND",
		Long: `Decode one OCPP-J frame, given as the argument or on stdin ("-"), with the framing
rules of --ocpp-version. A malformed frame exits 3.

The payload of a CALL or SEND is also validated against its schema; the result says
whether it is valid (payload_valid) and why not, without failing the command. A
CALLRESULT carries no action, so its payload cannot be validated from the frame alone:
use "ocppctl validate <Action> --response".`,
		Example: `
  ocppctl frame decode '[2,"19223201","BootNotification",{"chargePointVendor":"VendorX","chargePointModel":"SingleSocketCharger"}]' --ocpp-version 1.6

  # A frame copied from a CSMS log
  pbpaste | ocppctl frame decode - --ocpp-version 2.0.1 --format json
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := offline.FrameDecodeConfig{Version: "2.0.1", OutputFormat: "text"}
	runner.RegisterCommandCfg(cmd, &cfg, versionCompletion)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Frame = args[0]
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(context.Context) (any, error) { return offline.FrameDecode(&cfg, cmd.InOrStdin()) })
	}
	return cmd
}

func newFrameEncodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encode",
		Short: "Encode an OCPP-J frame",
		Long: `Build one OCPP-J frame from flags. The frame is checked by decoding it again with
the rules of --ocpp-version; the payload is not validated against a schema (use
"ocppctl validate" for that).`,
		Example: `
  ocppctl frame encode --type call --id 1 --action Heartbeat --ocpp-version 1.6

  ocppctl frame encode --type callerror --id 1 --error-code NotImplemented \
    --description "Unknown action" --ocpp-version 2.0.1

  # Only the frame, for a WebSocket tool
  ocppctl frame encode --type callresult --id 1 --payload '{"currentTime":"2026-01-01T00:00:00Z"}' --format json | jq -r .frame
`,
		Args: cobra.NoArgs,
	}
	cfg := offline.FrameEncodeConfig{Version: "2.0.1", OutputFormat: "text"}
	types := make([]string, 0, len(offline.FrameTypes))
	for t := range offline.FrameTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	runner.RegisterCommandCfg(cmd, &cfg, versionCompletion, func(c *cobra.Command) {
		_ = cli.RegisterEnumFlagCompletion(c, "type", types)
	})
	cli.MustMarkFlagRequired(cmd, "type")
	cli.MustMarkFlagRequired(cmd, "id")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(context.Context) (any, error) { return offline.FrameEncode(&cfg, cmd.InOrStdin()) })
	}
	return cmd
}

func newCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Certificate identifiers that OCPP messages carry",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	hash := &cobra.Command{
		Use:   "hash",
		Short: "Compute the CertificateHashData of a certificate",
		Long: `Compute CertificateHashData (OCPP 2.0.1 and 2.1 Part 2 §2.6, and the OCPP 1.6
Security Whitepaper): the hash of the issuer's name and key and the serial number, which
GetInstalledCertificateIds, DeleteCertificate and GetCertificateStatus use to name a
certificate. The "ocpp" object of the JSON output is the certificateHashData as
messages carry it.

The issuer is the certificate that signed --cert; a self-signed root is its own issuer.`,
		Example: `
  # The hash data of a station certificate
  ocppctl cert hash --cert station.pem --issuer sub-ca.pem

  # A DeleteCertificate payload for a root
  ocppctl cert hash --cert root.pem --format json | jq -c '{certificateHashData: .ocpp}'
`,
		Args: cobra.NoArgs,
	}
	cfg := offline.CertHashConfig{Hash: "sha256", OutputFormat: "text"}
	runner.RegisterCommandCfg(hash, &cfg, func(c *cobra.Command) {
		_ = cli.RegisterEnumFlagCompletion(c, "hash", []string{"sha256", "sha384", "sha512"})
		_ = c.MarkFlagFilename("cert", "pem", "crt", "cer")
		_ = c.MarkFlagFilename("issuer", "pem", "crt", "cer")
	})
	cli.MustMarkFlagRequired(hash, "cert")
	hash.RunE = func(c *cobra.Command, _ []string) error {
		return runner.RunFormattedChecked(c, cfg.OutputFormat, func() error { return nil },
			func(context.Context) (any, error) { return offline.CertHash(&cfg) })
	}
	cmd.AddCommand(hash)
	return cmd
}
