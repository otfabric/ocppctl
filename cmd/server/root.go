// SPDX-License-Identifier: MIT

// Package server holds the commands of a CSMS: `ocppctl server …`.
package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/config"
	"github.com/otfabric/ocppctl/internal/csms"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/format"
	"github.com/otfabric/ocppctl/internal/runner"
	"github.com/otfabric/ocppctl/internal/types"
)

// NewCmd returns the `server` command with all its subcommands.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Short: "Act as a CSMS: accept stations, answer them from a model, send them CALLs",
		Long: `Commands of a CSMS (in OCPP 1.6: a Central System) that stations connect to.

"server start" listens for stations, authenticates them, answers their CALLs from a model
(a YAML or JSON file, or the built-in one) and streams what happens as events. While it
runs, "server stations" lists the connected stations and "server call" sends one of them
a CALL, through the server's control socket. "server generate-config" writes a commented
model to start from.

Security profiles of the stations:
  0  ws://, no password                 --insecure-transport --no-auth
  1  ws://, HTTP Basic password         --insecure-transport and passwords
  2  wss://, HTTP Basic password        --tls-cert/--tls-key and passwords
  3  wss://, client certificate         --tls-cert/--tls-key and --tls-client-ca

Passwords come from the model (stations[].password) or --passwords-file, never from the
command line. See docs/server-config.md.`,
	}
	cmd.AddCommand(newStartCmd(), newCallCmd(), newStationsCmd(), newGenerateConfigCmd())
	return cmd
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Run a CSMS: accept stations, answer them from a model, stream events",
		Long: `Listen for charging stations and answer them from the model --config describes, or
the built-in model without it (see "server generate-config"): BootNotification Accepted
with an interval, Heartbeat with the time, Authorize against a token list, transactions
and notifications accepted. Every answer is checked against the OCA schema of the
station's version before it is sent.

Stations connect to ws[s]://host:port<path><identity>, offering one of the versions of
--ocpp-version. Authentication is required unless --no-auth: Basic passwords (from the
model or --passwords-file; user name = identity), client certificates (--tls-client-ca;
identity = certificate CN), or both. Listening without TLS needs --insecure-transport.

Every event is written to stdout as it happens: one line with --format text, one JSON
object per line with --format json, one table row with --format table. The events are
listening, connected, refused, call_received, answered, send_received (2.1), call_sent,
call_result, disconnected and stopping. With --debug every frame and the library's log
go to stderr.

The control socket (--control-socket, default ./ocppctl.sock or OCPPCTL_CONTROL_SOCKET)
lets "server call" and "server stations" reach the running server; it is created with
mode 0600 and removed on exit.

The server runs until interrupted (Ctrl-C), or for --duration.`,
		Example: `
  # A local CSMS for station testing: no TLS, no authentication
  ocppctl server start --insecure-transport --no-auth

  # Your own model, events as JSON Lines
  ocppctl server start --config csms.yaml --insecure-transport --format json

  # Security profile 2: TLS and Basic passwords from a file of identity:password lines
  ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key \
    --passwords-file passwords.txt --config csms.yaml

  # Security profile 3: stations present a certificate of this CA
  ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key --tls-client-ca ca.pem

  # OCPP 1.6 only, under /ocpp/, for 10 minutes
  ocppctl server start --ocpp-version 1.6 --path /ocpp/ --insecure-transport --no-auth --duration 10m

  # Watch the CALLs of one station
  ocppctl server start --insecure-transport --no-auth --format json |
    jq -c 'select(.identity=="CS001" and .event=="call_received") | {action, request}'
`,
		Args: cobra.NoArgs,
	}
	cfg := csms.NewStartConfig()
	runner.RegisterCommandCfg(cmd, &cfg, func(c *cobra.Command) {
		_ = cli.RegisterEnumFlagCompletion(c, "ocpp-version", []string{"2.1", "2.0.1", "1.6"})
		_ = c.MarkFlagFilename("config", "yaml", "yml", "json")
		_ = c.MarkFlagFilename("passwords-file")
		_ = c.MarkFlagFilename("control-socket", "sock")
		for _, f := range []string{"tls-cert", "tls-key", "tls-client-ca"} {
			_ = c.MarkFlagFilename(f, "pem", "crt", "cer", "key")
		}
	})
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg.Debug = cli.Debug(cmd)
		return runner.RunStream(cmd, cfg.OutputFormat,
			func() error { return csms.Check(&cfg) },
			func(ctx context.Context, out *format.StreamWriter) (int, error) {
				var writeErr error
				emit := func(e types.ServerEvent) {
					if err := out.Write(&e); err != nil && writeErr == nil {
						writeErr = err
					}
				}
				if err := csms.Run(ctx, &cfg, emit, cmd.ErrOrStderr(), nil); err != nil {
					return 0, err
				}
				if writeErr != nil {
					return 0, errs.Output(errs.CodeOutputWriteFailed, writeErr)
				}
				return errs.ExitOK, nil
			})
	}
	return cmd
}

func controlCompletion(c *cobra.Command) {
	_ = c.MarkFlagFilename("control-socket", "sock")
}

func newCallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call <Action>",
		Short: "Send a CALL to a station connected to a running server start",
		Long: `Have a running "ocppctl server start" send one CALL to a connected station, and print
the station's CALLRESULT.

The action must be one the CSMS sends in the station's version, and the payload is
validated against its OCA schema before it is sent (--raw sends it unvalidated, to test
how a station copes); the response is validated too.

A CALL that may change the station (anything but the Get* requests) needs --yes: the
station may be a real charger. Reset, RequestStartTransaction, RemoteStartTransaction,
ChangeAvailability, UnlockConnector, SetChargingProfile, UpdateFirmware and the like
act on the station as soon as it accepts them.

Exit codes: 2 without --yes; 3 for an unknown action or an invalid payload (nothing was
sent); 4 when the station is not connected (station_not_connected) or the control socket
cannot be reached (control_socket_failed); 5 when no response came in --timeout (for a
state-changing CALL: outcome_unknown, do not blindly resend); 6 for a CALLERROR or a
response that fails validation.`,
		Example: `
  # Read a 2.0.1 station's variables (no --yes: it changes nothing)
  ocppctl server call GetVariables --station CS001 \
    --payload '{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}'

  # Reset a station
  ocppctl server call Reset --station CS001 --yes --payload '{"type":"Immediate"}'

  # Start a charging session remotely: 2.x, then 1.6
  ocppctl server call RequestStartTransaction --station CS001 --yes \
    --payload '{"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}'
  ocppctl server call RemoteStartTransaction --station CP16 --yes --payload '{"idTag":"TESTTOKEN","connectorId":1}'

  # A server started in another directory
  OCPPCTL_CONTROL_SOCKET=/srv/csms/ocppctl.sock ocppctl server call TriggerMessage --station CS001 --yes \
    --payload '{"requestedMessage":"StatusNotification"}' --format json
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := csms.NewCallConfig()
	runner.RegisterCommandCfg(cmd, &cfg, controlCompletion, func(c *cobra.Command) { _ = c.MarkFlagFilename("payload", "json") })
	cli.MustMarkFlagRequired(cmd, "station")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Action = args[0]
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(ctx context.Context) (any, error) { return csms.Call(ctx, &cfg, cmd.InOrStdin()) })
	}
	return cmd
}

func newStationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stations",
		Short: "List the stations connected to a running server start",
		Long: `List the stations connected to a running "ocppctl server start", through its control
socket: identity, OCPP version, remote address, when it connected, and whether the
connection uses TLS and compression.`,
		Example: `
  ocppctl server stations
  ocppctl server stations --format table
  ocppctl server stations --format json | jq -r '.stations[].identity'
`,
		Args: cobra.NoArgs,
	}
	cfg := csms.NewControlConfig()
	runner.RegisterCommandCfg(cmd, &cfg, controlCompletion)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat, func() error { return nil },
			func(ctx context.Context) (any, error) { return csms.Stations(ctx, &cfg) })
	}
	return cmd
}

func newGenerateConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-config",
		Short: "Write a commented CSMS model to start from",
		Long: `Write the built-in CSMS model, the one "server start" uses without --config, with
comments that explain every field. Edit it and serve it with
"ocppctl server start --config <file>".

--format json writes the same model as JSON, without comments. An existing file is not
overwritten without --force.`,
		Example: `
  ocppctl server generate-config --output csms.yaml
  ocppctl server generate-config --format json --output csms.json
  ocppctl server generate-config            # to stdout
`,
		Args: cobra.NoArgs,
	}
	cfg := csms.GenerateConfig{Output: "-", Format: "yaml"}
	// Not runner.RegisterCommandCfg: --format is the format of the model here, not of stdout.
	config.MustLoadFromEnv(&cfg)
	config.RegisterFlags(cmd, &cfg)
	_ = cli.RegisterEnumFlagCompletion(cmd, "format", []string{"yaml", "json"})
	_ = cmd.MarkFlagFilename("output", "yaml", "yml", "json")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		data, err := csms.Generate(cfg.Format)
		if err != nil {
			return errs.InvalidInput(errs.CodeInvalidFormat, err.Error(), err)
		}
		if cfg.Output == "-" || cfg.Output == "" {
			if _, err := cmd.OutOrStdout().Write(data); err != nil {
				return errs.Output(errs.CodeOutputWriteFailed, err)
			}
			return nil
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if cfg.Force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		}
		f, err := os.OpenFile(cfg.Output, flags, 0o600) //nolint:gosec // the operator's own output file
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return errs.InvalidInput(errs.CodeOutputFileCreateFailed, fmt.Sprintf("%s exists: pass --force to overwrite it", cfg.Output), err)
			}
			return errs.Output(errs.CodeOutputFileCreateFailed, err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return errs.Output(errs.CodeOutputFileWriteFailed, err)
		}
		if err := f.Close(); err != nil {
			return errs.Output(errs.CodeOutputFileWriteFailed, err)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", cfg.Output)
		return nil
	}
	return cmd
}
