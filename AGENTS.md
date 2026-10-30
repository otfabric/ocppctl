# AGENTS.md — ocppctl

## What this is

Command-line toolkit for OCPP 1.6J, 2.0.1 and 2.1 built on [go-ocpp](https://github.com/otfabric/go-ocpp): probe a CSMS, send messages, simulate stations, run scenarios, serve a test CSMS. Pure Go, one binary. Profile: **cli**.

## Organisation standards

This repository follows **otfabric/engineering policy 1.1**, profile **cli** (`.otfabric/policy.yaml`, mode `enforce`). Where this file and the organisation standards disagree, this file wins here — say so in your report.

## Where the truth lives

| Question | Source |
|---|---|
| Exit codes (script-facing API) | docs/exitcodes.md |
| JSON output contract | docs/json-output.md |
| Commands and usage | README.md |
| How to contribute | CONTRIBUTING.md |
| Protocol behaviour | go-ocpp |
| Release history | RELEASE.md (`## vX.Y.Z` … `---`) |

## Hard rules

1. Exit codes and JSON output are a public contract: a change is called out in RELEASE.md and documented in docs/exitcodes.md or docs/json-output.md.
2. Never lower `COVERAGE_MIN` or the CI `coverage-min`, add lint suppressions without a reason, or skip or delete tests to make CI pass.
3. Never invent protocol semantics or claim interoperability or conformance beyond recorded evidence; protocol questions go to go-ocpp.
4. `github.com/otfabric/*` dependencies are pinned to released tags; no committed `replace` directives.
5. A state-changing `server call` requires `--yes` (exit 2 otherwise).

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| the command line, exit codes or `main` | `make e2e` (builds the binary and runs it) |
| anything concurrent, and before a release | `make stress` |
| interop-relevant behaviour | `scripts/interop.sh <ocpp-interop tag>` (Docker, jq) |

## Definition of Done

`make check` green; tests for new commands, error paths included; exit codes and JSON documented; RELEASE.md section for user-visible changes.

## Git and releases

Branches and PRs. Do not push to `main`, tag or run the Release workflow unless the maintainer asks in the session.
