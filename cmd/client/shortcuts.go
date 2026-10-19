// SPDX-License-Identifier: MIT

package client

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/clientops"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/payload"
)

// shortcutLong is the part of every shortcut's help that is the same.
const shortcutLong = `

The payload is built for the version the CSMS chooses, validated, and sent as by "client
call"; the result is the same. --payload-only prints the payload for the one
--ocpp-version given and connects to nothing, to start a "client call" payload from.

The command succeeds whenever the CSMS answers with a valid CALLRESULT, whatever status
it carries: check .response in the JSON output.`

// PayloadOnlyFlag is the flag every shortcut adds.
type PayloadOnlyFlag struct {
	PayloadOnly bool `flag:"payload-only" desc:"Print the payload for the one --ocpp-version given, and send nothing"`
}

func runShortcut(cmd *cobra.Command, c *clientops.Common, only bool, build func() clientops.Builder) error {
	return runOne(cmd, c, func(ctx context.Context) (any, error) {
		return clientops.Shortcut(ctx, c, only, build(), cmd.ErrOrStderr())
	})
}

func newBootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Send a BootNotification",
		Long:  "Send a BootNotification describing the station." + shortcutLong,
		Example: `
  ocppctl client boot --url wss://csms.example.com/ocpp/CS001 --vendor OTFabric --model Sim --serial SIM-0001

  # Is the station registered? Accepted, Pending or Rejected
  ocppctl client boot --url wss://csms.example.com/ocpp/CS001 --format json | jq -r .response.status
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		Vendor   string `flag:"vendor" desc:"Vendor name"`
		Model    string `flag:"model" desc:"Model"`
		Serial   string `flag:"serial" desc:"Serial number"`
		Firmware string `flag:"firmware" desc:"Firmware version"`
		Reason   string `flag:"reason" desc:"Boot reason (OCPP 2.x): PowerUp, ApplicationReset, FirmwareUpdate, LocalReset, RemoteReset, ScheduledReset, Triggered, Unknown, Watchdog"`
	}
	cfg.Common, cfg.Vendor, cfg.Model, cfg.Reason = clientops.NewCommon(), "OTFabric", "ocppctl", "PowerUp"
	wire(cmd, &cfg, enum("reason", "PowerUp", "ApplicationReset", "FirmwareUpdate", "LocalReset", "RemoteReset", "ScheduledReset", "Triggered", "Unknown", "Watchdog"))
	_ = cmd.Flags().MarkHidden("boot-vendor")
	_ = cmd.Flags().MarkHidden("boot-model")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if cfg.Boot {
			// Through the runner, so that --format json renders the error as JSON.
			return runOne(cmd, &cfg.Common, func(context.Context) (any, error) {
				return nil, errs.New(errs.KindUsage, errs.CodeInvalidFlagCombination, "--boot makes no sense with \"client boot\"", nil)
			})
		}
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.BootBuilder(payload.Boot{Vendor: cfg.Vendor, Model: cfg.Model, Serial: cfg.Serial, Firmware: cfg.Firmware, Reason: cfg.Reason})
		})
	}
	return cmd
}

func newHeartbeatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send a Heartbeat and print the CSMS's time",
		Long:  "Send a Heartbeat; the response carries the CSMS's current time." + shortcutLong,
		Example: `
  ocppctl client heartbeat --url wss://csms.example.com/ocpp/CS001 --boot
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
	}
	cfg.Common = clientops.NewCommon()
	wire(cmd, &cfg)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, clientops.HeartbeatBuilder)
	}
	return cmd
}

var tokenTypes = []string{"Central", "eMAID", "ISO14443", "ISO15693", "KeyCode", "Local", "MacAddress", "NoAuthorization"}

func newAuthorizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "authorize",
		Short: "Ask the CSMS whether an id token may charge",
		Long:  "Send an Authorize for an id token: idTag in OCPP 1.6, idToken with a type in 2.x." + shortcutLong,
		Example: `
  ocppctl client authorize --url wss://csms.example.com/ocpp/CS001 --id-token 04A1B2C3D4E5F6

  # 1.6: idTagInfo.status; 2.x: idTokenInfo.status
  ocppctl client authorize --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 \
    --id-token 04A1B2C3D4E5F6 --format json | jq -r .response.idTokenInfo.status
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		Token string `flag:"id-token" desc:"The id token (RFID UID, eMAID, ...)"`
		Type  string `flag:"type" desc:"Token type (OCPP 2.x)"`
	}
	cfg.Common, cfg.Type = clientops.NewCommon(), "ISO14443"
	wire(cmd, &cfg, enum("type", tokenTypes...))
	cli.MustMarkFlagRequired(cmd, "id-token")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.AuthorizeBuilder(payload.Authorize{Token: cfg.Token, Type: cfg.Type})
		})
	}
	return cmd
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Send a StatusNotification for a connector",
		Long: `Send a StatusNotification. OCPP 1.6 reports a connector (0 is the whole station) with
an error code; 2.x reports a connector of an EVSE.

1.6 statuses: Available, Preparing, Charging, SuspendedEVSE, SuspendedEV, Finishing,
Reserved, Unavailable, Faulted. 2.x statuses: Available, Occupied, Reserved,
Unavailable, Faulted.` + shortcutLong,
		Example: `
  ocppctl client status --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 --evse 1 --connector 1 --status Occupied

  ocppctl client status --url wss://csms.example.com/ocpp/CS001 --ocpp-version 1.6 --connector 1 \
    --status Faulted --error-code GroundFailure
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		EVSE      int    `flag:"evse" desc:"EVSE id (OCPP 2.x)"`
		Connector int    `flag:"connector" desc:"Connector id (in 1.6, 0 is the whole station)"`
		Status    string `flag:"status" desc:"Connector status"`
		ErrorCode string `flag:"error-code" desc:"Error code (OCPP 1.6)"`
	}
	cfg.Common, cfg.EVSE, cfg.Connector, cfg.ErrorCode = clientops.NewCommon(), 1, 1, "NoError"
	wire(cmd, &cfg,
		enum("status", "Available", "Occupied", "Preparing", "Charging", "SuspendedEVSE", "SuspendedEV", "Finishing", "Reserved", "Unavailable", "Faulted"),
		enum("error-code", "NoError", "ConnectorLockFailure", "EVCommunicationError", "GroundFailure", "HighTemperature", "InternalError",
			"LocalListConflict", "OtherError", "OverCurrentFailure", "OverVoltage", "PowerMeterFailure", "PowerSwitchFailure", "ReaderFailure",
			"ResetFailure", "UnderVoltage", "WeakSignal"))
	cli.MustMarkFlagRequired(cmd, "status")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.StatusBuilder(payload.Status{EVSE: cfg.EVSE, Connector: cfg.Connector, Status: cfg.Status, ErrorCode: cfg.ErrorCode})
		})
	}
	return cmd
}

func newMeterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meter",
		Short: "Send MeterValues with the energy register and, optionally, the power",
		Long: `Send a MeterValues with one sample: Energy.Active.Import.Register in Wh and, with
--power-w, Power.Active.Import in W. OCPP 1.6 reports a connector and, during a
transaction, its integer transaction id; 2.x reports an EVSE.` + shortcutLong,
		Example: `
  ocppctl client meter --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 --evse 1 --energy-wh 12500 --power-w 11000

  ocppctl client meter --url wss://csms.example.com/ocpp/CS001 --ocpp-version 1.6 --connector 1 \
    --energy-wh 12500 --transaction 42
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		EVSE        int     `flag:"evse" desc:"EVSE id (OCPP 2.x)"`
		Connector   int     `flag:"connector" desc:"Connector id (OCPP 1.6)"`
		EnergyWh    float64 `flag:"energy-wh" desc:"Energy register in Wh"`
		PowerW      float64 `flag:"power-w" desc:"Active power in W (0: not sent)"`
		Transaction string  `flag:"transaction" desc:"Transaction id (OCPP 1.6: an integer)"`
	}
	cfg.Common, cfg.EVSE, cfg.Connector = clientops.NewCommon(), 1, 1
	wire(cmd, &cfg)
	cli.MustMarkFlagRequired(cmd, "energy-wh")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.MeterBuilder(payload.Meter{EVSE: cfg.EVSE, Connector: cfg.Connector, EnergyWh: cfg.EnergyWh,
				PowerW: cfg.PowerW, Transaction: cfg.Transaction})
		})
	}
	return cmd
}

func newDataTransferCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-transfer",
		Short: "Send a vendor-specific DataTransfer",
		Long: `Send a DataTransfer. In OCPP 2.x --data is sent as JSON when it parses as JSON, as a
string otherwise; in 1.6 it is always a string.` + shortcutLong,
		Example: `
  ocppctl client data-transfer --url wss://csms.example.com/ocpp/CS001 --vendor com.example --message-id Ping --data '{"seq":1}'
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		Vendor    string `flag:"vendor" desc:"Vendor id (a reversed DNS name by convention)"`
		MessageID string `flag:"message-id" desc:"Message id"`
		Data      string `flag:"data" desc:"Data"`
	}
	cfg.Common = clientops.NewCommon()
	wire(cmd, &cfg)
	cli.MustMarkFlagRequired(cmd, "vendor")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.DataTransferBuilder(payload.DataTransfer{Vendor: cfg.Vendor, MessageID: cfg.MessageID, Data: cfg.Data})
		})
	}
	return cmd
}

func newTransactionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transaction",
		Short: "Start or stop a charging session",
		Long: `Start and stop a charging session: StartTransaction and StopTransaction in OCPP 1.6,
a TransactionEvent Started and Ended in 2.x.

In 1.6 the CSMS assigns the transaction id: it is .response.transactionId of "start", and
"stop" needs it. In 2.x the station assigns it: "start" generates one unless
--transaction is given, and prints it in .request.transactionInfo.transactionId.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newTransactionStartCmd(), newTransactionStopCmd())
	return cmd
}

func newTransactionStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a transaction",
		Long:  "Start a transaction on a connector for an id token." + shortcutLong,
		Example: `
  # OCPP 1.6: keep the id the CSMS assigns
  TX=$(ocppctl client transaction start --url wss://csms.example.com/ocpp/CS001 --ocpp-version 1.6 \
    --connector 1 --id-token 04A1B2C3D4E5F6 --format json | jq -r .response.transactionId)

  # OCPP 2.0.1: choose the id
  ocppctl client transaction start --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 \
    --evse 1 --connector 1 --id-token 04A1B2C3D4E5F6 --transaction tx-0001
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		EVSE        int     `flag:"evse" desc:"EVSE id (OCPP 2.x)"`
		Connector   int     `flag:"connector" desc:"Connector id"`
		Token       string  `flag:"id-token" desc:"The id token that starts the session"`
		Type        string  `flag:"type" desc:"Token type (OCPP 2.x)"`
		MeterStart  float64 `flag:"meter-start-wh" desc:"Energy register at the start, in Wh"`
		Transaction string  `flag:"transaction" desc:"Transaction id (OCPP 2.x; default: a random one)"`
	}
	cfg.Common, cfg.EVSE, cfg.Connector, cfg.Type = clientops.NewCommon(), 1, 1, "ISO14443"
	wire(cmd, &cfg, enum("type", tokenTypes...))
	cli.MustMarkFlagRequired(cmd, "id-token")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.StartBuilder(payload.TransactionStart{EVSE: cfg.EVSE, Connector: cfg.Connector, Token: cfg.Token,
				TokenType: cfg.Type, MeterStartWh: cfg.MeterStart, Transaction: cfg.Transaction})
		})
	}
	return cmd
}

func newTransactionStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop a transaction",
		Long:  "Stop a transaction with the final energy register." + shortcutLong,
		Example: `
  ocppctl client transaction stop --url wss://csms.example.com/ocpp/CS001 --ocpp-version 1.6 \
    --transaction "$TX" --energy-wh 15300 --reason Local

  ocppctl client transaction stop --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 \
    --evse 1 --transaction tx-0001 --energy-wh 15300 --seq-no 1
`,
		Args: cobra.NoArgs,
	}
	var cfg struct {
		clientops.Common
		PayloadOnlyFlag
		EVSE        int     `flag:"evse" desc:"EVSE id (OCPP 2.x)"`
		Connector   int     `flag:"connector" desc:"Connector id (OCPP 2.x)"`
		Transaction string  `flag:"transaction" desc:"Transaction id (OCPP 1.6: the integer the CSMS assigned)"`
		EnergyWh    float64 `flag:"energy-wh" desc:"Energy register at the end, in Wh"`
		Reason      string  `flag:"reason" desc:"Stop reason, e.g. Local, Remote, EVDisconnected, PowerLoss"`
		SeqNo       int     `flag:"seq-no" desc:"Sequence number of the TransactionEvent (OCPP 2.x; Started is 0)"`
	}
	cfg.Common, cfg.EVSE, cfg.Connector, cfg.SeqNo = clientops.NewCommon(), 1, 1, 1
	wire(cmd, &cfg)
	cli.MustMarkFlagRequired(cmd, "transaction")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runShortcut(cmd, &cfg.Common, cfg.PayloadOnly, func() clientops.Builder {
			return clientops.StopBuilder(payload.TransactionStop{EVSE: cfg.EVSE, Connector: cfg.Connector, Transaction: cfg.Transaction,
				MeterStopWh: cfg.EnergyWh, Reason: cfg.Reason, SeqNo: cfg.SeqNo})
		})
	}
	return cmd
}
