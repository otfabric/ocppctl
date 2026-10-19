# ocppctl — design

Status: **delivered in v0.1.0**, except the `example` command (see [Phases](#phases)).
This document was the design proposal; it now records what was built and the decisions
taken on its open questions. The user documentation is the [README](../README.md) and the
other files in this directory.

ocppctl is the command-line tool of the OT Fabric family for OCPP (Open
Charge Point Protocol) 1.6J, 2.0.1 and 2.1, built on
[go-ocpp](https://github.com/otfabric/go-ocpp). Like iec104ctl and
modbusctl it covers both roles: as a **Charging Station** (`client`) it
probes, exercises and simulates a station against any CSMS; as a **CSMS**
(`server`) it lets real stations connect to something that answers.

## Contents

- [Goals and non-goals](#goals-and-non-goals)
- [Who uses it, for what](#who-uses-it-for-what)
- [Command tree](#command-tree)
- [Connecting](#connecting)
- [Sending a message](#sending-a-message)
- [The simulated station (`client run`)](#the-simulated-station-client-run)
- [Scenarios (`client script`)](#scenarios-client-script)
- [Offline tools](#offline-tools)
- [The CSMS (`server`)](#the-csms-server)
- [Output, errors and exit codes](#output-errors-and-exit-codes)
- [Security](#security)
- [What go-ocpp provides, and what it needs to add](#what-go-ocpp-provides-and-what-it-needs-to-add)
- [Repository, build, tests](#repository-build-tests)
- [Phases](#phases)
- [Open questions, and the decisions taken](#open-questions-and-the-decisions-taken)

## Goals and non-goals

Goals:

- Talk OCPP to any CSMS or station from a terminal or a CI job, in every
  version and security profile go-ocpp supports, with nothing to write but a
  command line or a small YAML file.
- Be strict by default: every message validated against the OCA schemas in
  both directions, the way go-ocpp is. Sending something invalid is an
  explicit choice (`--raw`), for testing how a CSMS copes with it.
- Be scriptable: stable JSON output, JSON Lines streams, documented exit
  codes, one result per command.
- Look and behave like its siblings (iec104ctl, bacnetctl, modbusctl): same
  command shape, flags, environment variables, output formats, exit codes,
  docs, Makefile and release workflow.

Non-goals:

- Not a CSMS product: no persistence, users, tariffs or billing. The
  `server` is a simulator for testing stations.
- No charging logic beyond what a simulator needs (a transaction that
  meters energy at a fixed power). Real behaviour is the application's, as
  in go-ocpp.
- No protocol code of its own: everything on the wire is go-ocpp's (its
  REQUIREMENTS.md R13 makes this a requirement of go-ocpp's public API).

## Who uses it, for what

| Who | Task | Commands |
|---|---|---|
| CSMS developer | Check that a CSMS accepts a station and answers each message | `client probe`, `client call`, `client boot` |
| CSMS QA / CI | Run a station through a scenario and assert the answers | `client script` |
| Field engineer | Is the CSMS reachable, which version and security profile does it accept, is the certificate right? | `client probe` |
| Integrator | Simulate one or many stations for a demo or a load test | `client run`, `client run --url …/CS{001..100}` |
| Station / firmware developer | Point a real station at something that answers and see what it sends | `server start`, `server call` |
| Anyone | Check or build a payload without a connection | `validate`, `actions`, `frame`, `--payload-only` |

## Command tree

Nested by role, as iec104ctl and modbusctl:

```
ocppctl
├── client                         a Charging Station
│   ├── probe                      connect, negotiate, report (version, TLS, auth, compression, latency)
│   ├── call <Action>              send one CALL with a JSON payload, print the response
│   ├── send <Action>              send one SEND (OCPP 2.1), no response
│   ├── boot | heartbeat | authorize | status | meter | data-transfer
│   │                              shortcuts for the common CALLs, payload built from flags
│   ├── transaction start|stop     a charging session (1.6 Start/StopTransaction, 2.x TransactionEvent)
│   ├── run                        simulated stations: boot, heartbeat, answer the CSMS from a model
│   ├── script <file>              run a scenario of steps with expectations (CI)
│   └── generate-config            write a commented station model
├── server                         a CSMS
│   ├── start                      accept stations, answer them from a model, stream events
│   ├── call <Action>              send a CALL to a station connected to a running `server start`
│   ├── stations                   list the stations connected to a running `server start`
│   └── generate-config            write a commented CSMS model
├── actions                        list the actions of a version with their direction
├── validate <Action>              validate a request or response payload against the OCA schema
├── frame decode|encode            the OCPP-J frame codec
├── cert hash                      CertificateHashData of a certificate (ocppcert)
├── completion                     bash, zsh, fish, powershell
└── version
```

`example <Action>` (a schema-valid payload to start from) was proposed and is
deferred: see [What go-ocpp provides](#what-go-ocpp-provides-and-what-it-needs-to-add).

`client call` is the general mechanism; the shortcuts exist because a
BootNotification typed on a command line is tedious. Every shortcut is
`client call` with a payload built from flags, and prints the same result.

## Connecting

The target is a URL, the way OCPP-J defines it: the CSMS's endpoint plus the
station identity as the last path segment.

```
ocppctl client probe --url wss://csms.example.com/ocpp/CS001
```

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `--url` | `OCPPCTL_URL` | required | `ws://` or `wss://`, identity last; `ws://` needs `--insecure-transport` |
| `--ocpp-version` | `OCPPCTL_OCPP_VERSION` | `2.1,2.0.1,1.6` | Versions to offer, in order (`1.6`, `2.0.1`, `2.1`); the CSMS picks one |
| `--password-file` | `OCPPCTL_PASSWORD_FILE`; `OCPPCTL_PASSWORD` (the password itself) | — | HTTP Basic password (profiles 1, 2), from a file or the environment, never on the command line; the user name is the identity. Read at run time, never printed |
| `--tls-ca`, `--tls-cert`, `--tls-key` | `OCPPCTL_TLS_*` | — | PEM files: trust roots; client certificate for profile 3 |
| `--tls-server-name` | `OCPPCTL_TLS_SERVER_NAME` | URL host | Name to verify the CSMS certificate against |
| `--insecure-transport` | `OCPPCTL_INSECURE_TRANSPORT` | off | Allow `ws://` (profile 0, 1) |
| `--compression` | `OCPPCTL_COMPRESSION` | off | Offer permessage-deflate |
| `--header NAME=VALUE` | — | — | Extra handshake header (repeatable) |
| `--timeout` | `OCPPCTL_TIMEOUT` | `30s` | Response timeout of a CALL |
| `--connect-timeout` | `OCPPCTL_CONNECT_TIMEOUT` | `10s` | Dial, TLS and WebSocket handshake |
| `--debug` (global) | — | off | Every frame sent and received (go-ocpp's frame observer) and go-ocpp's log, to stderr |

The flag is `--ocpp-version`, not `--version`, so that it cannot be taken for
the version of ocppctl. The proposed `--trace` became the global `--debug`
of the sibling tools, which also prints the causes of an error.

There is no `--tls-insecure`: go-ocpp does not allow TLS verification to be
turned off, and ocppctl does not either. `--tls-ca` is the way to trust a
test CA.

### `client probe`

Connects once and reports what a support engineer asks first, without
sending an OCPP message:

```
$ ocppctl client probe --url wss://localhost:19193/CS001 --tls-ca ca.pem --tls-cert cs001.pem --tls-key cs001.key
target          wss://localhost:19193/CS001
identity        CS001
offered         2.1, 2.0.1, 1.6
negotiated      2.1
tls             TLS 1.3, TLS_AES_128_GCM_SHA256, certificate CN=localhost (issuer CN=Test Root CA, valid until 2027-10-18), client certificate sent
authentication  certificate
compression     not negotiated
handshake       7 ms
```

With `--each-version` it connects once per version and reports which the CSMS
accepts. A refusal is a result, not just an error: HTTP 401 (credentials),
404 (unknown identity), 409 (already connected), 503 (full) and "no common
subprotocol" are reported with their meaning (go-ocpp's
`client.HandshakeError`). The command fails only when no attempt is accepted.

## Sending a message

```
ocppctl client call BootNotification --url … --payload '{"reason":"PowerUp","chargingStation":{"vendorName":"OT Fabric","model":"Test"}}'
ocppctl client call MeterValues --url … --payload @meter.json
cat request.json | ocppctl client call DataTransfer --url … --payload -
```

- **Connect, send, print, disconnect.** One CALL per invocation. The CSMS's
  own CALLs meanwhile are answered with the first schema-valid answer
  (`{}` or status Accepted, else NotSupported), and listed in the result.
- **Validation.** The request is validated against the schema of the
  negotiated version before sending (before connecting when one version is
  offered); an invalid one is exit 3 and nothing is sent. The response is
  validated too; a response that fails is reported (exit 6,
  `invalid_response`) with the field and the reason.
- **`--raw`** sends the payload as given, without validating it: for testing
  how a CSMS handles invalid requests. The response is still validated and
  reported.
- **Sequence.** Many CSMSs reject anything before an accepted
  BootNotification. `--boot` sends one first (vendor and model from
  `--boot-vendor`, `--boot-model`) and fails with `boot_not_accepted` (exit 6)
  if it is not Accepted.
- **Side effects.** A request that may change state and gets no answer is
  `outcome_unknown` (exit 5), as in bacnetctl: it is never sent twice.

Result (`--format json`):

```json
{
  "target": "wss://csms.example.com/ocpp/CS001",
  "version": "2.0.1",
  "action": "BootNotification",
  "request": {"reason": "PowerUp", "chargingStation": {"vendorName": "OT Fabric", "model": "Test"}},
  "response": {"status": "Accepted", "currentTime": "2026-10-18T19:00:00.000Z", "interval": 300},
  "duration_ms": 38,
  "validated": true,
  "received": []
}
```

The proposed `message_id` was dropped from the result (it is in the
`--debug` trace); `validated` says whether the request was checked.

A CALLERROR is exit 6 with an `ocpp` detail:
`{"error":{"kind":"call_error","code":"call_error","message":"…"},"ocpp":{"error_code":"NotSupported","description":"…","details":{}}}`.

### Shortcuts

| Command | Version-aware payload from flags |
|---|---|
| `client boot --vendor --model [--serial --firmware --reason]` | BootNotification (1.6 and 2.x shapes) |
| `client heartbeat` | Heartbeat |
| `client authorize --id-token X [--type ISO14443]` | Authorize (`idTag` in 1.6, `idToken` in 2.x) |
| `client status --connector N --status Available [--evse N --error-code]` | StatusNotification |
| `client meter --energy-wh N [--power-w N --connector N --evse N --transaction ID]` | MeterValues |
| `client data-transfer --vendor V [--message-id M --data D]` | DataTransfer |
| `client transaction start --connector N --id-token X` / `stop --transaction ID --energy-wh N` | 1.6 Start/StopTransaction, 2.x TransactionEvent Started/Ended |

Each shortcut has `--payload-only`, which prints the payload it would send
for the one `--ocpp-version` given and exits: the shortcut doubles as a
payload builder for `client call`.

## The simulated station (`client run`)

A long-running station, the counterpart of iec104ctl's `server start`:

```
ocppctl client run --url wss://csms.example.com/ocpp/CS001 --config station.yaml --format json
```

The model flag is `--config`, as for the siblings' simulators, not the
proposed `--model`.

- Connects, sends BootNotification until Accepted (honouring the CSMS's
  interval and Pending), then StatusNotification for every connector, then
  Heartbeat at the interval the CSMS gave (or the model's).
- Answers every CALL from the CSMS: the model's answer per action (an
  object, or `accept`, `reject`, `not-supported`, `not-implemented`), else a
  built-in behaviour, else the first schema-valid default. Built: GetVariables
  /SetVariables/GetBaseReport and GetConfiguration/ChangeConfiguration over
  the model's device model and configuration keys; RemoteStart and
  RequestStartTransaction start a simulated session, RemoteStop and
  RequestStopTransaction end it; Reset ends the sessions, reconnects and
  boots with reason RemoteReset; TriggerMessage sends the triggered message;
  and fixed answers for a station without a local list, monitors, tariffs or
  customer data. Not built: SetChargingProfile is accepted but not stored,
  and GetCompositeSchedule is not computed from it.
- Reconnects with the OCPP back-off of go-ocpp, unless `--no-reconnect`;
  keepalive with `--ping-interval`.
- Writes events to stdout as JSON Lines (or text, or table rows):
  `connected`, `booted`, `boot_pending`, `boot_rejected`, `call_sent`,
  `call_result`, `call_error`, `call_received`, `answered`, `send_received`,
  `session_started`, `session_ended`, `reset`, `disconnected`,
  `reconnecting`, `error`. Ends on Ctrl-C or `--duration`.
- A `{N..M}` range in the identity of `--url` (`--url …/ocpp/CS{001..100}`)
  runs one station per number, `--stagger` apart, for load tests; a summary
  of calls, latencies and errors on stderr at the end. This replaced the
  proposed `--stations N`: the identities are explicit, and one flag fewer.

The model is YAML or JSON, strict (unknown fields are errors, every problem
reported before connecting), with a built-in default when none is given, and
`client generate-config` writes a commented one. Its reference is
[station-model.md](station-model.md).

## Scenarios (`client script`)

For CI: a scenario is a list of steps with expectations, and the command's
exit code says whether all held.

```yaml
version: "2.0.1"
steps:
  - call: BootNotification
    payload: {reason: PowerUp, chargingStation: {vendorName: OT Fabric, model: Test}}
    expect: {status: Accepted}
  - call: Authorize
    payload: {idToken: {idToken: 04A1B2C3D4E5F6, type: ISO14443}}
    expect: {idTokenInfo.status: Accepted}
  - wait_for: RequestStartTransaction      # a CALL from the CSMS
    timeout: 60s
    answer: {status: Accepted}
  - call: TransactionEvent
    payload: "@tx-started.json"
  - expect_no_call: 10s                    # the CSMS stays silent
```

Steps: `call`, `send`, `wait_for` (a CALL from the CSMS, with an answer and
optional expectations on its payload), `expect_no_call`, `sleep`, `disconnect`,
`reconnect`. Expectations match fields by path (equality, a regular
expression, presence, or one of a set); `save` keeps a field as a variable
for later payloads. The result lists every step with pass or fail; exit 0
when all pass, 7 when some fail. The reference is [scenarios.md](scenarios.md).

This is the go-ocpp interop harness idea turned into a user tool: the same
"fixture sample and expected answer" model, for one CSMS under test.

## Offline tools

No connection needed:

- `actions --ocpp-version 2.1` lists every action with its sender (station,
  csms, both) and kind (call, send), from go-ocpp's `Actions`.
- `validate <Action> --ocpp-version 2.0.1 [--response] --payload @x.json`
  runs go-ocpp's validation; exit 0, or 3 with the violation (field, rule,
  CALLERROR code) in the result.
- `frame decode '[2,"1","Heartbeat",{}]' --ocpp-version 1.6` and `frame
  encode` use `ocppj`.
- `cert hash --cert station.pem --issuer ca.pem [--hash sha256]` prints
  CertificateHashData (`ocppcert`).
- `example <Action>` is deferred (see below).

## The CSMS (`server`)

```
ocppctl server start --listen :8443 --tls-cert … --tls-key … --tls-client-ca … --config csms.yaml
```

- Accepts stations of every version in `--ocpp-version`, authenticates them
  (Basic passwords per identity, client certificates, or none with
  `--no-auth`), answers their CALLs from the model (BootNotification
  Accepted, Pending or Rejected with an interval, Authorize against a token
  list, 1.6 transaction ids, per-action overrides), and streams events
  (`listening`, `connected`, `refused`, `call_received`, `answered`,
  `send_received`, `call_sent`, `call_result`, `disconnected`, `stopping`) as
  JSON Lines.
- `server call <Action> --station CS001 --payload …` sends a CALL to a
  station connected to a running `server start`, through a control socket
  (`--control-socket`, a Unix socket, the same idea as ocpp-interop's
  container contract). `server stations` lists the sessions.
- CALLs that may change a station (everything but the read-only Get…
  requests) need `--yes`, as bacnetctl's `--yes-i-am-sure`: the station may
  be a real charger.
- `--max-stations`, `--allow CIDR`, `--ping-interval`, `--rate-limit` and
  `--rate-burst` map to go-ocpp's server options.

The reference is [server-config.md](server-config.md).

## Output, errors and exit codes

As the siblings:

- `--format text|json|table` (`OCPPCTL_OUTPUT_FORMAT`), default `text`.
  stdout carries only results; progress, connection state and `--debug`
  frames go to stderr. Streams are JSON Lines. No colour.
- JSON field names are snake_case, durations `*_ms`, lists never null,
  results have `target`. Payloads are the OCPP JSON as on the wire (camelCase
  inside `request`/`response`). The contract is documented in
  [json-output.md](json-output.md).
- Fatal error envelope `{"error":{"kind","code","message"},"ocpp":{…}}`.

| Exit | Kind | OCPP cases |
|---|---|---|
| 0 | ok | |
| 2 | usage | bad flags, unknown command, `server call` without `--yes` |
| 3 | invalid input | invalid payload (schema), unknown action for the version, bad model or scenario, unreadable certificate; nothing was sent |
| 4 | transport | dial or TLS failure, handshake refused (HTTP 401/404/409/503, in `ocpp.http_status`), no common subprotocol, connection lost, station not connected |
| 5 | timeout | no response in time; `outcome_unknown` for a state-changing CALL |
| 6 | call_error, protocol | CALLERROR from the peer, response that fails validation, boot not accepted |
| 7 | partial | `client script` with failed steps; `client run` with some stations failing |
| 10 | output or internal | |

The mapping follows go-ocpp's sentinels (`ErrConnectFailed`,
`HandshakeError`, `ErrTimeout`, `CallError`, `ValidationError`,
`ErrConnectionLost`), so ocppctl adds no protocol judgement of its own. The
reference is [exitcodes.md](exitcodes.md).

## Security

- Passwords from `--password-file` or `OCPPCTL_PASSWORD`, never echoed,
  never in `--help` defaults, never in output or traces (go-ocpp never logs
  credentials; ocppctl's `--debug` shows frames, not the handshake headers).
- TLS verification cannot be turned off. `--tls-ca` for test CAs.
- The server requires authentication unless `--no-auth`, and TLS unless
  `--insecure-transport`; its control socket is created with mode 0600.
- [SECURITY.md](../SECURITY.md): OCPP controls real chargers; `server call`
  and `client run` against production systems need authorization; a
  responsible-use note in the README as in iec104ctl.

## What go-ocpp provides, and what it needs to add

Already public and enough for everything built: `client` and `server` with
every option (TLS, Basic auth, compression, keepalive, reconnect, rate
limits), raw `Call` and `Send` with `json.RawMessage`, `ValidateRequest` and
`ValidateResponse` per version, `Actions` with sender and SEND flag,
`ocppj` decode and encode, `FrameObserver` and interceptors for `--debug` and
events, `HandshakeError` / `CloseError` / `CallError` for exit codes,
`ocppcert`, and `ocpptest` for ocppctl's own tests. v0.1.0 is built on
go-ocpp v0.1.1.

Proposed additions to go-ocpp, none of which v0.1.0 needed:

1. **Example payloads** for `example`: go-ocpp generates them in its tests
   (`internal/schematest.Instance`), but that package is internal and pulls
   in a test-only validator. Proposed: an exported, dependency-free
   `ocpp16.Example(action, response, full bool) json.RawMessage` (and in
   ocpp201, ocpp21) generated by `internal/gen` at build time. Until it
   exists, `example` is deferred; `--payload-only` and `examples/` fill the
   gap.
2. **Typed decoding by name**: `newRequest`/`newResponse` are unexported.
   `client run` answers through raw dispatch, so this is optional; exporting
   `NewRequest(action)`/`NewResponse(action)` would let ocppctl pretty-print
   typed payloads and apply model defaults safely.
3. **The schema text** for `validate --explain`: an exported
   `Schema(action, response bool) []byte` over the embedded OCA files (the
   CC BY-ND files are distributed unmodified, ADR 0007).

## Repository, build, tests

The iec104ctl/bacnetctl skeleton:

- `main.go` with `-X main.version/tag/commit/buildDate`, `cmd/` (cobra, a
  fresh tree per `NewRootCmd()`, `Run(ctx, args, stdin, stdout, stderr) int`,
  `SilenceUsage`/`SilenceErrors`, only `--debug` global), `internal/{config,
  errs, format, runner, cli, types, conn, clientops, payload, answer, station,
  script, csms, offline, ocppx, ocpperr, testcsms}`.
- Config structs with `env`/`flag`/`desc` tags (`OCPPCTL_*`), flag >
  environment > default, `[env: …]` in help.
- Docs: README (the family's structure, "OCPP in two minutes"),
  `docs/{exitcodes.md, json-output.md, station-model.md, scenarios.md,
  server-config.md}`, `RELEASE.md`, `SECURITY.md`, `CONTRIBUTING.md`.
- Makefile, `ci.yml` and `release.yml` calling the shared
  `otfabric/.github` workflows, seven platforms, `COVERAGE_MIN` 85.
- Tests in process through `Run`, against go-ocpp's `ocpptest` CSMS and
  stations (every command, every version, TLS profiles, error paths and exit
  codes), and `scripts/interop.sh`, which runs ocppctl in both roles against
  the reference implementations of ocpp-interop (Docker images), outside the
  test suite.

## Phases

| Phase | Scope | Status |
|---|---|---|
| 1 — client essentials | skeleton, `client probe`, `client call`, `client send`, shortcuts, `actions`, `validate`, `frame`, `cert hash`, `version`, `completion`; exit codes and JSON contract; README and docs | delivered in v0.1.0 |
| 2 — simulated station | `client run` with the station model, built-in behaviours, sessions, reconnect; `client generate-config`; identity ranges | delivered in v0.1.0 |
| 3 — scenarios | `client script` with steps and expectations | delivered in v0.1.0 |
| 3 — `example` | `example <Action>`, after go-ocpp exports example payloads | deferred |
| 4 — CSMS | `server start` with a model, control socket, `server call`, `server stations`, `server generate-config` | delivered in v0.1.0 |
| 5 — release | v0.1.0 | released with phases 1 to 4 together |

## Open questions, and the decisions taken

1. **Command shape**: nested by role or flat verbs? **Nested by role**, as
   iec104ctl and modbusctl: `call` means the same thing in both roles
   (`client call`, `server call`).
2. **Default versions offered**: `2.0.1,1.6`, or all three with 2.1 first?
   **All three, newest first: `2.1,2.0.1,1.6`.** A CSMS that supports
   several picks the newest; one that does not support 2.1 picks what it
   has. `--ocpp-version` narrows it.
3. **`client call` and BootNotification**: send it only when asked
   (`--boot`), or always first unless `--no-boot`? **Only when asked
   (`--boot`).** A one-shot command sends exactly the message it is given;
   `--boot` exists for CSMSs that refuse anything before an accepted boot,
   and fails with `boot_not_accepted` when it is not.
4. **Scenario format**: YAML as proposed, or reuse ocpp-interop's fixture
   format? **ocppctl's own YAML**, with expectations, variables and
   `wait_for`, which the fixture format does not have. The fixture samples
   are used by `scripts/interop.sh` instead, through `client call` and
   `server call`.
5. **Load testing** (`--stations N`): in phase 2, or a separate feature?
   **In phase 2, as identity ranges** (`--url …/CS{001..100}`, `--stagger`)
   with a summary on stderr (stations, booted, failed, calls, errors, average
   and maximum latency, sessions). Percentiles and throughput reporting are
   left for later; the JSON event stream has what they need.
