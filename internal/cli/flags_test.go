// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestDebug(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	sub := &cobra.Command{Use: "sub"}
	root.AddCommand(sub)
	if Debug(sub) {
		t.Error("no --debug flag reads as on")
	}
	root.PersistentFlags().Bool("debug", false, "")
	if Debug(sub) {
		t.Error("--debug off reads as on")
	}
	if err := root.PersistentFlags().Set("debug", "true"); err != nil {
		t.Fatal(err)
	}
	if !Debug(sub) {
		t.Error("--debug on reads as off")
	}
}

func TestMustMarkFlagRequired(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("name", "", "")
	MustMarkFlagRequired(cmd, "name")
	if err := cmd.ValidateRequiredFlags(); err == nil {
		t.Error("the flag is not required")
	}
	defer func() {
		if recover() == nil {
			t.Error("marking a flag that does not exist did not panic")
		}
	}()
	MustMarkFlagRequired(cmd, "absent")
}
