// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"strings"

	"github.com/otfabric/go-ocpp"
	"github.com/otfabric/go-ocpp/ocppj"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/otfabric/ocppctl/internal/ocppx"
)

// versionsOf returns the OCPP versions --ocpp-version of cmd names, or every version when
// it is not given (or names none that parses).
func versionsOf(cmd *cobra.Command) []ocpp.Version {
	var list []string
	if f := cmd.Flags().Lookup("ocpp-version"); f != nil && f.Changed {
		if s, ok := f.Value.(pflag.SliceValue); ok {
			list = s.GetSlice()
		} else {
			list = []string{f.Value.String()}
		}
	}
	if v, err := ocppx.ParseVersions(list); err == nil {
		return v
	}
	v, _ := ocppx.ParseVersions(ocppx.DefaultVersions)
	return v
}

// CompleteActions completes the <Action> argument with the actions of the versions
// --ocpp-version names (all versions without it) that keep accepts.
func CompleteActions(keep func(ocppx.Action) bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return actionNames(cmd, keep, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// RegisterActionFlagCompletion completes the flag name of cmd with the actions, as
// [CompleteActions] does for an argument.
func RegisterActionFlagCompletion(cmd *cobra.Command, name string, keep func(ocppx.Action) bool) {
	_ = cmd.RegisterFlagCompletionFunc(name, func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return actionNames(cmd, keep, toComplete), cobra.ShellCompDirectiveNoFileComp
	})
}

func actionNames(cmd *cobra.Command, keep func(ocppx.Action) bool, toComplete string) []string {
	var out []string
	for _, v := range versionsOf(cmd) {
		for _, a := range ocppx.Actions(v) {
			if (keep == nil || keep(a)) && strings.HasPrefix(strings.ToLower(a.Name), strings.ToLower(toComplete)) {
				out = append(out, a.Name)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// RegisterErrorCodeCompletion completes the flag name of cmd with the CALLERROR codes of
// the versions --ocpp-version names.
func RegisterErrorCodeCompletion(cmd *cobra.Command, name string) {
	_ = cmd.RegisterFlagCompletionFunc(name, func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var out []string
		for _, v := range versionsOf(cmd) {
			for _, c := range ocppj.ErrorCodes(v) {
				if strings.HasPrefix(strings.ToLower(string(c)), strings.ToLower(toComplete)) {
					out = append(out, string(c))
				}
			}
		}
		slices.Sort(out)
		return slices.Compact(out), cobra.ShellCompDirectiveNoFileComp
	})
}
