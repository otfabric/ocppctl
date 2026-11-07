// SPDX-License-Identifier: MIT

// Package cmd is the command tree of ocppctl.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/otfabric/ocppctl/cmd/client"
	"github.com/otfabric/ocppctl/cmd/server"
	"github.com/otfabric/ocppctl/internal/buildinfo"
	clihelp "github.com/otfabric/ocppctl/internal/cli"
	"github.com/otfabric/ocppctl/internal/config"
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
	// Every other flag and argument completes no file names, except these files.
	clihelp.CompleteRest(root, map[string][]string{
		"config": {"yaml", "yml", "json"}, "output": nil, "password-file": nil, "passwords-file": nil, "control-socket": nil,
		"tls-ca": pemFiles, "tls-cert": pemFiles, "tls-key": pemFiles, "tls-client-ca": pemFiles,
	}, map[string][]string{"client script": {"yaml", "yml"}})
	return root
}

// pemFiles are the extensions of the certificate and key files the TLS flags take.
var pemFiles = []string{"pem", "crt", "cer", "key"}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of ocppctl",
		Long: `Print the version of ocppctl on one line: the version, the git tag and commit it
was built from, the build date, the Go version and the platform. --json (or
OCPPCTL_FORMAT=json) prints the same as one JSON object.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Resolve("ocppctl", buildMeta.version, buildMeta.tag, buildMeta.commit, buildMeta.buildDate)
			line := info.String()
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON || strings.EqualFold(strings.TrimSpace(config.Getenv("OCPPCTL_FORMAT")), "json") {
				line = info.JSON()
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	cmd.Flags().Bool("json", false, "Print the version as one JSON object")
	return cmd
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
