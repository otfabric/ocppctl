// SPDX-License-Identifier: MIT

package client

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/clientops"
)

func newCallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call <Action>",
		Short: "Send one CALL to the CSMS and print its response",
		Long: `Connect, send one CALL with the payload given, print the CALLRESULT, and disconnect.

The payload is validated against the OCA schema of the version the CSMS chose before it
is sent, and the response after it arrives; --raw sends the payload unvalidated, to test
how a CSMS handles a bad message (the response is still validated). With one
--ocpp-version the payload is checked before connecting.

Many CSMSs refuse every message until the station has booted: --boot sends a
BootNotification first and requires it to be Accepted.

A CALLERROR answer exits 6 with its code, description and details in the "ocpp" object of
the error. When no response arrives in --timeout, a CALL that changes state (anything but
Heartbeat, Authorize and the Get* requests) exits 5 with code outcome_unknown: the CSMS
may have acted on it, so do not blindly resend it.`,
		Example: `
  # A BootNotification in OCPP 2.0.1
  ocppctl client call BootNotification --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 \
    --payload '{"reason":"PowerUp","chargingStation":{"vendorName":"OTFabric","model":"Sim"}}'

  # A payload from a file, after booting
  ocppctl client call StatusNotification --url wss://csms.example.com/ocpp/CS001 --boot --payload @status.json

  # A shortcut as payload builder
  ocppctl client meter --ocpp-version 1.6 --connector 1 --energy-wh 1500 --payload-only |
    ocppctl client call MeterValues --url ws://localhost:9000/CS001 --insecure-transport --ocpp-version 1.6 --payload -

  # A deliberately invalid message
  ocppctl client call Heartbeat --url ws://localhost:9000/CS001 --insecure-transport --raw --payload '{"bogus":1}'
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := clientops.CallConfig{Common: clientops.NewCommon()}
	wire(cmd, &cfg)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Action = args[0]
		return runOne(cmd, &cfg.Common, func(ctx context.Context) (any, error) {
			return clientops.Call(ctx, &cfg, cmd.InOrStdin(), cmd.ErrOrStderr())
		})
	}
	return cmd
}

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <Action>",
		Short: "Send one SEND (OCPP 2.1) to the CSMS",
		Long: `Connect, send one SEND message (OCPP 2.1's unanswered messages, such as
NotifyPeriodicEventStream), and disconnect once it is written. There is no response: the
result says only that it was sent. The CSMS must choose OCPP 2.1.`,
		Example: `
  ocppctl client send NotifyPeriodicEventStream --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.1 \
    --payload '{"id":1,"pending":0,"basetime":"2026-01-01T00:00:00Z","data":[{"t":0,"v":"230"}]}'
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := clientops.SendConfig{Common: clientops.NewCommon()}
	cfg.Versions = []string{"2.1"}
	wire(cmd, &cfg)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Action = args[0]
		return runOne(cmd, &cfg.Common, func(ctx context.Context) (any, error) {
			return clientops.Send(ctx, &cfg, cmd.InOrStdin(), cmd.ErrOrStderr())
		})
	}
	return cmd
}
