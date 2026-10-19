# Contributing to ocppctl

Thank you for your interest in contributing to `ocppctl`! This document covers the guidelines for contributing.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```sh
   git clone git@github.com:<your-username>/ocppctl.git
   cd ocppctl
   ```
3. Create a feature branch:
   ```sh
   git checkout -b feature/my-change
   ```

## Development Setup

### Prerequisites

- Go 1.26+ (see [go.mod](go.mod))
- [staticcheck](https://staticcheck.dev/) and [golangci-lint](https://golangci-lint.run/) for linting
- No charger and no CSMS: the test suite brings its own (see [Testing](#testing))
- For the interoperability run only: Docker and jq

### Build & Test

```sh
# Run all checks (format, lint, vet, race tests, coverage gate)
make check

# Run tests with the race detector
make test

# Run tests with whole-module coverage (fails below 85%)
make coverage

# Run every test 20 times at 1, 2 and 8 CPUs with the race detector (before a release,
# and after touching anything concurrent); STRESS_COUNT=50 for more
make stress

# Build for the current platform (./bin/ocppctl); runs `make check` first
make build

# Run against the reference implementations of ocpp-interop (Docker, jq)
scripts/interop.sh v0.3.1
```

Run `make` (or `make help`) for the full list of targets.

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Use meaningful variable and function names
- Add comments only where the logic isn't self-evident; comments end with a period (enforced by the linter)

### Protocol Behaviour

- Everything on the wire is go-ocpp's. ocppctl adds no protocol code of its own: no framing, no schema handling, no message types
- Do not invent OCPP behaviour. What the simulated station and the CSMS do must be what the OCPP specification says, or a documented choice where it leaves the decision to the implementation (`docs/station-model.md`, `docs/server-config.md`). If you are not sure, leave it to the model
- Every message ocppctl sends or answers is validated against the OCA schema of the session's version; keep it that way

### Formatting

`make fmt` formats with the `gofmt` of the Go version in `go.mod`, which is the version CI uses, not necessarily the one you have installed. `make fmt-check` (part of `make check`) fails if either that `gofmt` or your local one would change a file: `gofmt` output differs slightly between Go releases, and a file the two disagree on fails CI. If they disagree, restructure the code (for example, move trailing comments onto their own lines) rather than picking a side.

### Commit Messages

Use clear, concise commit messages:

```
component: short description

Optional longer explanation of the change, why it was made,
and any relevant context.
```

Examples:
- `run: answer GetLocalListVersion`
- `script: report the path of a failed expectation`
- `docs: document the station model`

### Testing

- Add tests for new functionality in the appropriate `_test.go` file
- Tests run in process. For anything that talks to a CSMS, use [`internal/testcsms`](internal/testcsms), which serves go-ocpp's `ocpptest` CSMS on a free loopback port with schema-valid answers for every version and records what it received. For anything that talks to a station, use go-ocpp's `ocpptest` stations or ocppctl's own `client run`. Command tests run the real command tree through `cmd.Run` and assert on stdout, stderr and the exit code
- No network beyond the loopback interface, and no external services: the interoperability run (`scripts/interop.sh`) is separate from the test suite
- Assert behaviour (output, exit code, what the peer received), not just that the code ran
- Tests must not write files into the repository: use `t.TempDir()`
- Do not assume timing a peer does not promise: wait for an event with a deadline instead of sleeping
- Keep tests fast and race-clean; CI runs them with `-race`
- Whole-module statement coverage must stay at or above 85% (`make coverage`)
- Tests must not depend on timing: wait for what the code under test reports (an event, a line on stdout or stderr, a message the peer received) with a generous deadline, never for a fixed sleep. A CSMS in a test sends one CALL at a time, as OCPP-J requires. `make stress` must pass
- If your change affects what goes on the wire or how ocppctl answers, run `scripts/interop.sh` and say in the pull request what you tested against

### Documentation

When you change commands, flags, output or exit codes, update:

- The command's `Short` / `Long` / `Example` help text
- [README.md](README.md), including the environment variable table for new flags
- [docs/json-output.md](docs/json-output.md) and [docs/exitcodes.md](docs/exitcodes.md) — `--format json` and exit codes are a stable contract for scripts
- [docs/station-model.md](docs/station-model.md) and the commented built-in model in `internal/station/model.go` when the station model or its behaviour changes
- [docs/scenarios.md](docs/scenarios.md) when the scenario format changes
- [docs/server-config.md](docs/server-config.md) and the commented model in `internal/csms/model.go` when the CSMS changes
- [RELEASE.md](RELEASE.md), calling out anything that breaks existing usage

## Submitting Changes

1. Push your branch to your fork:
   ```sh
   git push origin feature/my-change
   ```
2. Open a Pull Request against the `main` branch
3. Describe what your change does and why
4. Reference any related issues

For larger changes, please open an issue first to discuss the approach.

## Reporting Issues

- Use GitHub Issues to report bugs or suggest features
- Include the output of `ocppctl version` in bug reports
- Provide the exact command that triggered the issue, along with the error output; `--debug` adds every frame sent and received (remove id tokens and other data you do not want to publish)
- Name the CSMS or station on the other side, its version, and the OCPP version and security profile used
- Report security vulnerabilities privately: see [SECURITY.md](SECURITY.md)

## Project Structure

```
cmd/                  Root command, offline commands (actions, validate, frame, cert), completion, version
cmd/client/           Charging Station commands (probe, call, send, shortcuts, transaction, run, script, generate-config)
cmd/server/           CSMS commands (start, call, stations, generate-config)
internal/config/      Flag and environment binding
internal/conn/        The connection to a CSMS: URL, versions, credentials, TLS, frame trace
internal/clientops/   The one-shot client commands
internal/payload/     The payloads of the shortcuts, per version
internal/answer/      Default answers to CALLs that arrive meanwhile
internal/station/     The simulated station: model, validation, behaviour
internal/script/      Scenarios: format, matchers, variables, runner
internal/csms/        The CSMS: model, answers, control socket
internal/offline/     actions, validate, frame, cert hash
internal/ocppx/       Versions, actions and schema validation over go-ocpp
internal/ocpperr/     go-ocpp errors as typed errors with exit codes
internal/testcsms/    An in-process CSMS for tests
internal/format/      Output formatting (text, JSON, table, streaming)
internal/types/       Output data models
internal/runner/      Command wiring, formatted and streaming runs, fatal rendering
internal/errs/        Typed errors, stable codes, exit-code mapping
internal/cli/         Shared Cobra helpers
examples/             Scenarios and payloads
scripts/interop.sh    Interoperability run against ocpp-interop
docs/                 Exit codes, JSON output, station model, scenarios, CSMS, design
```

## License

By contributing to `ocppctl`, you agree that your contributions will be licensed under the [MIT License](LICENSE).
