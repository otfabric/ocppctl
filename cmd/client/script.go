// SPDX-License-Identifier: MIT

package client

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/conn"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/runner"
	"github.com/otfabric/ocppctl/internal/script"
	"github.com/otfabric/ocppctl/internal/types"
)

func newScriptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "script <scenario.yaml>",
		Short: "Run a scenario of steps with expectations against a CSMS (for CI)",
		Long: `Connect as a Charging Station and run the steps of a scenario, checking what the CSMS
answers and sends. The exit code says whether every step passed: 0 when all did, 7 when
some failed (their results say why), 4 when the first connection failed, 3 when the
scenario itself is invalid (every problem is reported before connecting).

Steps (docs/scenarios.md has the full reference):

  call: <Action>           send a CALL; payload, expect, expect_error, save, timeout
  send: <Action>           send a SEND (OCPP 2.1); payload
  wait_for: <Action>       wait for a CALL from the CSMS; expect (on its request),
                           answer, save, timeout
  expect_no_call: <dur>    the CSMS sends nothing for this long
  sleep: <dur>             wait
  disconnect: true         close the connection
  reconnect: true          connect again

expect matches fields of the response (or request) by path, e.g. idTokenInfo.status or
getVariableResult[0].attributeStatus: a value means equality, {regex: ...} a regular
expression, {present: true|false} presence, {one_of: [...]} a set. save stores a field as
a variable that later payloads use as ${name}; ${now}, ${uuid} and ${identity} are built
in, and --var NAME=VALUE adds more.

The scenario's version (version: "2.0.1") is the one offered; without it, --ocpp-version
applies. After the first failing step the remaining steps are skipped, unless
--keep-going. CALLs from the CSMS that no step waits for are answered with a schema-valid
default and listed as unexpected.`,
		Example: `
  # Run a scenario against a CSMS under test
  ocppctl client script boot-authorize.yaml --url wss://csms.test/ocpp/CS001 --password-file pw.txt

  # In CI: JSON result, every step run
  ocppctl client script regression.yaml --url ws://csms:9000/CI001 --insecure-transport --keep-going --format json > result.json

  # A token from the pipeline
  ocppctl client script authorize.yaml --url wss://csms.test/ocpp/CS001 --var token=04A1B2C3D4E5F6
`,
		Args: cobra.ExactArgs(1),
	}
	cfg := script.Config{Target: conn.NewTarget(), OutputFormat: "text"}
	wire(cmd, &cfg)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg.Debug, cfg.File = cli.Debug(cmd), args[0]
		var sc *script.Scenario
		var p *conn.Parsed
		return runner.RunFormattedChecked(cmd, cfg.OutputFormat,
			func() (err error) { sc, p, err = script.Prepare(&cfg); return err },
			func(ctx context.Context) (any, error) { return script.Run(ctx, &cfg, sc, p, cmd.ErrOrStderr()) },
			runner.WithSuccessExit(func(v any) int {
				if r, ok := v.(*types.ScriptResult); ok && r.Failed > 0 {
					return errs.ExitPartial
				}
				return errs.ExitOK
			}))
	}
	return cmd
}
