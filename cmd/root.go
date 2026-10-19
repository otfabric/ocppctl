// SPDX-License-Identifier: MIT

// Package cmd is the command tree of ocppctl.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/cmd/client"
	"github.com/otfabric/ocppctl/cmd/server"
	"github.com/otfabric/ocppctl/internal/errs"
	"github.com/otfabric/ocppctl/internal/runner"
)

// buildMeta holds link-time metadata set from main via SetBuildMeta.
var buildMeta struct {
	version, tag, commit, buildDate string
}

// SetBuildMeta is called from main before Execute; values come from ldflags -X main.*.
func SetBuildMeta(version, tag, commit, buildDate string) {
	buildMeta.version = version
	buildMeta.tag = tag
	buildMeta.commit = commit
	buildMeta.buildDate = buildDate
}

// NewRootCmd builds the command tree. Every call returns a fresh tree, with the flags of
// every command at the defaults the environment gives them.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ocppctl",
		Short: "OCPP 1.6J, 2.0.1 and 2.1 command-line Charging Station and CSMS",
		Long: `ocppctl speaks OCPP-J, the protocol between EV charging stations and their central
system (CSMS), in versions 1.6, 2.0.1 and 2.1, in both roles.

As a Charging Station ("ocppctl client") it probes a CSMS, sends single messages, runs a
simulated station from a model, and runs scenarios with expectations for CI. As a CSMS
("ocppctl server") it accepts stations, answers them from a model and sends them CALLs.
"actions", "validate", "frame" and "cert" work offline.

Results go to stdout as text, a table or JSON (--format); progress and diagnostics go to
stderr. Exit codes are stable: see docs/exitcodes.md.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().Bool("debug", false, "Trace every frame sent and received, and the library's log, to stderr; show the causes of an error")
	root.AddCommand(
		client.NewCmd(),
		server.NewCmd(),
		newActionsCmd(),
		newValidateCmd(),
		newFrameCmd(),
		newCertCmd(),
		newCompletionCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of ocppctl",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			ver := strings.TrimSpace(buildMeta.version)
			if ver == "" {
				ver = "(unknown)"
			}
			_, _ = fmt.Fprintf(out, "ocppctl version: %s\n", ver)
			if s := strings.TrimSpace(buildMeta.tag); s != "" {
				_, _ = fmt.Fprintf(out, "tag:         %s\n", s)
			}
			if s := strings.TrimSpace(buildMeta.commit); s != "" {
				_, _ = fmt.Fprintf(out, "commit:      %s\n", s)
			}
			if s := strings.TrimSpace(buildMeta.buildDate); s != "" {
				_, _ = fmt.Fprintf(out, "build date:  %s\n", s)
			}
			_, _ = fmt.Fprintf(out, "go version:  %s\n", runtime.Version())
			_, _ = fmt.Fprintf(out, "platform:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}

// Execute runs the root command and returns the process exit code (main should call os.Exit).
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// Run executes ocppctl with args, reading stdin and writing to stdout and stderr, and
// returns the exit code. It is Execute without the process: what the tests drive.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root, err := buildRoot()
	if err != nil {
		return runner.RenderFatal(nil, err, stdout, stderr, false)
	}
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	inv := &runner.Invocation{}
	ctx = runner.WithInvocation(ctx, inv)
	err = root.ExecuteContext(ctx)
	debug, _ := root.PersistentFlags().GetBool("debug")
	if err != nil {
		return runner.RenderFatal(inv, usageError(err), stdout, stderr, debug)
	}
	if inv.SuccessExitSet {
		return inv.SuccessExitCode
	}
	return errs.ExitOK
}

// buildRoot is NewRootCmd with a malformed environment variable reported as invalid input
// instead of a panic: the configuration of every command is read when the tree is built.
func buildRoot() (root *cobra.Command, err error) {
	defer func() {
		if r := recover(); r != nil {
			msg := strings.TrimPrefix(fmt.Sprint(r), "ocppctl: ")
			err = errs.InvalidInput(errs.CodeInvalidInput, msg, nil)
		}
	}()
	return NewRootCmd(), nil
}

// usageError classifies what Cobra itself rejects (an unknown command or flag, a missing
// required flag, a value a flag cannot hold) as a usage error: exit code 2.
func usageError(err error) error {
	if _, ok := errs.As(err); ok {
		return err
	}
	return errs.New(errs.KindUsage, errs.CodeInvalidInput, err.Error(), err)
}
