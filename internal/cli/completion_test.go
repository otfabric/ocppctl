// SPDX-License-Identifier: MIT

package cli

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func complete(t *testing.T, cmd *cobra.Command, flag, prefix string) []string {
	t.Helper()
	fn, ok := cmd.GetFlagCompletionFunc(flag)
	if !ok {
		t.Fatalf("no completion registered for --%s", flag)
	}
	got, dir := fn(cmd, nil, prefix)
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, file completion must stay off", dir)
	}
	return got
}

func TestEnumCompletion(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("format", "", "")
	if err := RegisterEnumFlagCompletion(cmd, "format", []string{"text", "json", "Table"}); err != nil {
		t.Fatal(err)
	}
	for prefix, want := range map[string][]string{
		"":     {"text", "json", "Table"},
		"t":    {"text", "Table"},
		" TA ": {"Table"},
		"x":    {},
	} {
		if got := complete(t, cmd, "format", prefix); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v, want %v", prefix, got, want)
		}
	}
	if err := RegisterEnumFlagCompletion(cmd, "absent", nil); err == nil {
		t.Error("a flag that does not exist was accepted")
	}
}

func TestEnumCompletionWithDescriptions(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("mode", "", "")
	desc := map[string]string{"zeta": "last", "alpha": "first", "Beta": ""}
	if err := RegisterEnumFlagCompletionWithDescriptions(cmd, "mode", desc); err != nil {
		t.Fatal(err)
	}
	for prefix, want := range map[string][]string{
		"":   {"Beta", "alpha\tfirst", "zeta\tlast"},
		"B":  {"Beta"},
		" a": {"alpha\tfirst"},
		"q":  nil,
	} {
		if got := complete(t, cmd, "mode", prefix); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q, want %q", prefix, got, want)
		}
	}
}
