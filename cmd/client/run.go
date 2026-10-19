// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/config"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/format"
	"github.com/otfabric/ocppctl/internal/runner"
	"github.com/otfabric/ocppctl/internal/station"
	"github.com/otfabric/ocppctl/internal/types"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a simulated Charging Station against a CSMS",
		Long: `Connect a simulated Charging Station, described by the model --config (or the built-in
one), and keep it running:

  - It sends BootNotification until the CSMS accepts it, waiting the interval the CSMS
    gives while it is Pending or Rejected, then a StatusNotification for every connector,
    then Heartbeat at the CSMS's interval (or the model's heartbeat_interval).
  - It answers the CSMS's CALLs: from the model's answers first, then with its built-in
    behaviour (RemoteStart/StopTransaction and RequestStart/StopTransaction start and stop
    a simulated session; Reset ends the sessions, reconnects and boots with reason
    RemoteReset; TriggerMessage sends the message; GetConfiguration/ChangeConfiguration
    (1.6) and GetVariables/SetVariables/GetBaseReport (2.x) work on the model's
    configuration and device model), and otherwise with the first schema-valid answer.
  - It charges the sessions of the model: a start, a meter sample every meter_interval,
    and a stop, as StartTransaction/MeterValues/StopTransaction in 1.6 and
    TransactionEvent Started/Updated/Ended in 2.x.
  - A lost connection is reconnected with the OCPP back-off, unless --no-reconnect; the
    station stays booted and reports its connectors again.

Every event (connected, booted, boot_pending, boot_rejected, call_sent, call_result,
call_error, call_received, answered, send_received, session_started, session_ended,
reset, disconnected, reconnecting, error) is written to stdout as it happens: a line with --format text, a
JSON object per line with --format json, a table row with --format table. With --debug
every frame is traced to stderr.

A {N..M} range in the identity of --url runs one station per number, for load tests:
--url wss://csms/ocpp/CS{001..100} runs CS001 to CS100, --stagger apart. A summary of
calls, latency and errors goes to stderr at the end; the exit code is 7 when some
stations failed and the others ran.

The station runs until interrupted (Ctrl-C) or for --duration. The first connection must
succeed: a refused or failing one exits 4 at once.`,
		Example: `
  # The built-in station, against a local CSMS, events as JSON Lines
  ocppctl client run --url ws://localhost:9000/CS001 --insecure-transport --format json

  # Your own model, OCPP 2.0.1 only, security profile 2, for ten minutes
  OCPPCTL_PASSWORD=s3cret ocppctl client run --url wss://csms.example.com/ocpp/CS001 \
    --ocpp-version 2.0.1 --config station.yaml --duration 10m

  # 100 stations, 50 ms apart, for a load test
  ocppctl client run --url wss://csms.example.com/ocpp/LOAD{001..100} --stagger 50ms --duration 15m --format json > events.jsonl

  # Only the transactions
  ocppctl client run --url ws://localhost:9000/CS001 --insecure-transport --format json |
    jq -c 'select(.event | startswith("session_"))'
`,
		Args: cobra.NoArgs,
	}
	cfg := station.NewRunConfig()
	wire(cmd, &cfg, func(c *cobra.Command) { _ = c.MarkFlagFilename("config", "yaml", "yml", "json") })
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg.Debug = cli.Debug(cmd)
		var p *station.Prepared
		return runner.RunStream(cmd, cfg.OutputFormat,
			func() (err error) { p, err = station.Prepare(&cfg); return err },
			func(ctx context.Context, out *format.StreamWriter) (int, error) {
				var mu sync.Mutex
				var writeErr error
				emit := func(e types.StationEvent) {
					mu.Lock()
					defer mu.Unlock()
					if err := out.Write(&e); err != nil && writeErr == nil {
						writeErr = err
					}
				}
				code, err := station.Run(ctx, &cfg, p, emit, cmd.ErrOrStderr())
				if err != nil {
					return 0, err
				}
				if writeErr != nil {
					return 0, errs.Output(errs.CodeOutputWriteFailed, writeErr)
				}
				return code, nil
			})
	}
	return cmd
}

// GenerateConfig configures `client generate-config`.
type GenerateConfig struct {
	Output string `flag:"output" env:"OCPPCTL_OUTPUT" desc:"File to write, or - for stdout"`
	Format string `flag:"format" desc:"Model format: yaml (with comments) or json"`
	Force  bool   `flag:"force" desc:"Overwrite an existing --output file"`
}

func newGenerateConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-config",
		Short: "Write an example station model to start from",
		Long: `Write the model of the built-in simulated station: two EVSEs, a session it charges by
itself, a device model (2.x), configuration keys (1.6) and a few answers. Edit it and run
it with "ocppctl client run --config <file>". docs/station-model.md describes every field.

The YAML form carries comments that explain every section; --format json writes the same
model as JSON. An existing file is not overwritten without --force.`,
		Example: `
  ocppctl client generate-config --output station.yaml
  ocppctl client generate-config --format json --output station.json
  ocppctl client generate-config            # to stdout
`,
		Args: cobra.NoArgs,
	}
	cfg := GenerateConfig{Output: "-", Format: "yaml"}
	// Not runner.RegisterCommandCfg: --format is the format of the model here, not of stdout.
	config.MustLoadFromEnv(&cfg)
	config.RegisterFlags(cmd, &cfg)
	_ = cli.RegisterEnumFlagCompletion(cmd, "format", []string{"yaml", "json"})
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		var data []byte
		switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
		case "", "yaml", "yml":
			data = []byte(station.DemoYAML)
		case "json":
			var err error
			if data, err = station.Demo().Encode(); err != nil {
				return errs.Output(errs.CodeJSONEncodeFailed, err)
			}
		default:
			return errs.InvalidInput(errs.CodeInvalidFormat, fmt.Sprintf("invalid --format %q (expected: yaml, json)", cfg.Format), nil)
		}
		return WriteFile(cmd, cfg.Output, cfg.Force, data)
	}
	return cmd
}

// WriteFile writes data to stdout ("-") or to a new file, which --force may overwrite.
func WriteFile(cmd *cobra.Command, path string, force bool, data []byte) error {
	if path == "-" || path == "" {
		if _, err := cmd.OutOrStdout().Write(data); err != nil {
			return errs.Output(errs.CodeOutputWriteFailed, err)
		}
		return nil
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600) //nolint:gosec // the operator's own output file
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errs.InvalidInput(errs.CodeOutputFileCreateFailed, fmt.Sprintf("%s exists: pass --force to overwrite it", path), err)
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
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", path)
	return nil
}
