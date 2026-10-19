// SPDX-License-Identifier: MIT

// Package client holds the commands of a Charging Station: `ocppctl client …`.
package client

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/clientops"
	"github.com/otfabric/ocppctl/internal/runner"
)

// NewCmd returns the `client` command with all its subcommands.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		Short: "Act as a Charging Station: probe a CSMS, send messages, simulate a station, run scenarios",
		Long: `Commands that connect to a CSMS as a Charging Station does.

The target is --url: the CSMS endpoint followed by the station identity as the last path
segment, ws[s]://host[:port]/path/<identity>. --ocpp-version lists the versions to offer,
newest first by default (2.1, 2.0.1, 1.6); the CSMS picks one.

Security profiles:
  0  ws://, no password                 --insecure-transport
  1  ws://, HTTP Basic password         --insecure-transport and --password-file
  2  wss://, HTTP Basic password        --password-file (and --tls-ca for a private CA)
  3  wss://, client certificate         --tls-cert and --tls-key

The password is read from --password-file or the OCPPCTL_PASSWORD environment variable,
never from the command line; the Basic user name is the identity. TLS verification cannot
be turned off: trust a test CA with --tls-ca.

probe, call, send and the shortcuts connect, do one thing and disconnect. CALLs the CSMS
sends meanwhile are answered with the first schema-valid answer (an empty object or
status Accepted) and listed in the result. run keeps a simulated station connected;
script runs a scenario.`,
	}
	cmd.AddCommand(
		newProbeCmd(),
		newCallCmd(),
		newSendCmd(),
		newBootCmd(),
		newHeartbeatCmd(),
		newAuthorizeCmd(),
		newStatusCmd(),
		newMeterCmd(),
		newDataTransferCmd(),
		newTransactionCmd(),
		newRunCmd(),
		newScriptCmd(),
		newGenerateConfigCmd(),
	)
	return cmd
}

// connCompletion completes the flags every client command has.
func connCompletion(cmd *cobra.Command) {
	if cmd.Flags().Lookup("ocpp-version") != nil {
		_ = cli.RegisterEnumFlagCompletion(cmd, "ocpp-version", []string{"2.1", "2.0.1", "1.6"})
	}
	for _, f := range []string{"tls-ca", "tls-cert", "tls-key"} {
		if cmd.Flags().Lookup(f) != nil {
			_ = cmd.MarkFlagFilename(f, "pem", "crt", "cer", "key")
		}
	}
	if cmd.Flags().Lookup("password-file") != nil {
		_ = cmd.MarkFlagFilename("password-file")
	}
}

// enum registers shell completion for a flag with a fixed set of values.
func enum(flag string, values ...string) func(*cobra.Command) {
	return func(cmd *cobra.Command) { _ = cli.RegisterEnumFlagCompletion(cmd, flag, values) }
}

// wire loads cfg from the environment, binds its flags to cmd and registers completions.
func wire(cmd *cobra.Command, cfg any, hooks ...func(*cobra.Command)) {
	runner.RegisterCommandCfg(cmd, cfg, append([]func(*cobra.Command){connCompletion}, hooks...)...)
}

// runOne is the RunE of a command with one result.
func runOne(cmd *cobra.Command, c *clientops.Common, collect func(ctx context.Context) (any, error)) error {
	c.Debug = cli.Debug(cmd)
	return runner.RunFormattedChecked(cmd, c.OutputFormat, func() error { return nil }, collect)
}
