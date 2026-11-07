// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

func TestActionAndErrorCodeCompletion(t *testing.T) {
	newCmd := func(slice bool) *cobra.Command {
		cmd := &cobra.Command{Use: "x"}
		if slice {
			cmd.Flags().StringSlice("ocpp-version", nil, "")
		} else {
			cmd.Flags().String("ocpp-version", "2.0.1", "")
		}
		cmd.Flags().String("action", "", "")
		cmd.Flags().String("error-code", "", "")
		RegisterActionFlagCompletion(cmd, "action", func(a ocppx.Action) bool { return a.From != ocppx.FromCSMS })
		RegisterErrorCodeCompletion(cmd, "error-code")
		return cmd
	}

	cmd := newCmd(true)
	_ = cmd.Flags().Set("ocpp-version", "1.6")
	got, dir := CompleteActions(nil)(cmd, nil, "boot")
	if !slices.Equal(got, []string{"BootNotification"}) || dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("1.6 actions: %v %v", got, dir)
	}
	if got, _ := CompleteActions(nil)(cmd, []string{"Heartbeat"}, ""); got != nil {
		t.Errorf("a second argument completes: %v", got)
	}
	fn, _ := cmd.GetFlagCompletionFunc("error-code")
	if got, _ := fn(cmd, nil, "Format"); !slices.Equal(got, []string{"FormationViolation"}) {
		t.Errorf("1.6 error codes: %v", got)
	}

	// Without --ocpp-version, every version; a CSMS action is not offered to a station.
	cmd = newCmd(false)
	fn, _ = cmd.GetFlagCompletionFunc("action")
	got, _ = fn(cmd, nil, "")
	if !slices.Contains(got, "BootNotification") || !slices.Contains(got, "StartTransaction") || !slices.Contains(got, "TransactionEvent") ||
		slices.Contains(got, "RemoteStartTransaction") {
		t.Errorf("station actions of every version: %v", got)
	}
	fn, _ = cmd.GetFlagCompletionFunc("error-code")
	if got, _ := fn(cmd, nil, "Format"); !slices.Equal(got, []string{"FormatViolation", "FormationViolation"}) {
		t.Errorf("error codes of every version: %v", got)
	}

	// A version that does not parse falls back to every version.
	_ = cmd.Flags().Set("ocpp-version", "9.9")
	if got, _ := CompleteActions(nil)(cmd, nil, "NotifyPeriodicEventStream"); len(got) != 1 {
		t.Errorf("bad version: %v", got)
	}
}
