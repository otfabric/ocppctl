# Scenarios: `ocppctl client script`

`ocppctl client script <scenario.yaml>` connects as a Charging Station and runs the steps of a scenario against a CSMS: it sends CALLs and checks the answers, waits for CALLs from the CSMS and answers them, and checks that the CSMS stays silent when it should. The result lists every step with pass or fail, and the exit code says whether all passed. It is meant for regression tests of a CSMS in CI.

- [Running a scenario](#running-a-scenario)
- [The scenario file](#the-scenario-file)
- [Steps](#steps)
- [Expectations](#expectations)
- [Variables](#variables)
- [Payload files](#payload-files)
- [CALLs from the CSMS](#calls-from-the-csms)
- [Result and exit codes](#result-and-exit-codes)
- [The examples](#the-examples)
- [In CI](#in-ci)

## Running a scenario

```console
ocppctl client script examples/scenarios/smoke-1.6.yaml --url ws://localhost:9000/CS001 --insecure-transport --var token=04A1B2C3D4E5F6
```

```
connected to ws://localhost:9000/CS001 as CS001 (OCPP 1.6)
pass 1 call BootNotification
…
scenario smoke-1.6 against ws://localhost:9000/CS001 (OCPP 1.6)
PASS     1  boot: call BootNotification (0 ms)
PASS     2  call StatusNotification (0 ms)
PASS     3  call Heartbeat (0 ms)
PASS     4  call Authorize (0 ms)
PASS     5  start: call StartTransaction (0 ms)
PASS     6  call MeterValues (0 ms)
PASS     7  stop: call StopTransaction (0 ms)
7 passed, 0 failed, 0 skipped in 4 ms
```

The progress lines (`pass 1 call BootNotification`) go to stderr as each step ends; the result goes to stdout, as text, a table or JSON (`--format`).

Besides the connection flags every `client` command has (`--url`, `--ocpp-version`, `--password-file`, `--tls-*`, `--insecure-transport`, `--compression`, `--header`, `--timeout`, `--connect-timeout`):

| Flag | Meaning |
|---|---|
| `--keep-going` | run the remaining steps after a step fails (default: skip them) |
| `--var NAME=VALUE` | a variable for `${NAME}` in payloads (repeatable); its value is a string |
| `--format` | the result as text, table or JSON (`OCPPCTL_OUTPUT_FORMAT`) |

`--timeout` (default 30s) is the default timeout of `call` and `wait_for` steps.

## The scenario file

```yaml
name: smoke-2.0.1          # optional; default: the file name without extension
version: "2.0.1"           # optional; the version to offer
steps:
  - call: BootNotification
    payload: {reason: PowerUp, chargingStation: {vendorName: OT Fabric, model: Simulator}}
    expect: {status: Accepted}
```

| Field | Meaning |
|---|---|
| `name` | the name in the result; default: the file name without its extension |
| `version` | `"1.6"`, `"2.0.1"` or `"2.1"` (quote it: YAML reads `2.1` as a number). When set, it is the only version offered, whatever `--ocpp-version` says, and every step's action is checked against it before connecting. Without it, the versions of `--ocpp-version` are offered and the CSMS picks one |
| `steps` | the steps, at least one |

The file is YAML (or JSON). Decoding is strict: a field ocppctl does not know is an error. The scenario is checked completely before connecting, and every problem reported (exit 3).

## Steps

Each step is exactly one of the following kinds, with the fields that kind takes:

| Step | Fields | Passes when |
|---|---|---|
| `call: <Action>` | `payload`, `raw`, `expect` or `expect_error`, `save`, `timeout` | the CSMS answers with a valid CALLRESULT that meets `expect` (or with the CALLERROR of `expect_error`) |
| `send: <Action>` | `payload`, `raw` | the SEND (OCPP 2.1) is handed to the connection. A SEND is never answered |
| `wait_for: <Action>` | `expect`, `answer`, `save`, `timeout` | the CSMS sends a CALL of this action within `timeout`, and its request meets `expect` |
| `expect_no_call: <duration>` | | the CSMS sends no CALL and no SEND for this long |
| `sleep: <duration>` | | always, after waiting this long |
| `disconnect: true` | | the connection is closed |
| `reconnect: true` | | a new connection is made (the old one is closed first, if open) |

Every step may also have a `name`, shown in the result.

| Field | Meaning |
|---|---|
| `name` | a label for the result |
| `payload` | the request of a `call` or `send`: an object, or `"@file"` (see [Payload files](#payload-files)). Default `{}` |
| `raw` | send the payload without validating it against the schema (for testing how a CSMS handles an invalid request). The response is still validated |
| `expect` | conditions on the response (`call`) or on the request (`wait_for`); see [Expectations](#expectations) |
| `expect_error` | `call` only: the CALLERROR code the CSMS must answer with (such as `NotSupported`), or `any`. A CALLRESULT then fails the step. Excludes `expect` |
| `answer` | `wait_for` only: the response to send: an object, `"@file"`, or `accept` (`{"status":"Accepted"}`), `reject` (`{"status":"Rejected"}`), `not-supported` (the CALLERROR `NotSupported`), `not-implemented` (the CALLERROR `NotImplemented`). Default: `{"status":"Accepted"}` when the action's response schema accepts it, else `{}`, else the CALLERROR `NotSupported` |
| `save` | `name: path` pairs: store a field of the response (`call`) or request (`wait_for`) as a variable. A path that is absent fails the step |
| `timeout` | how long a `call` waits for its response, or a `wait_for` for its CALL (default `--timeout`) |

Durations are Go durations: `500ms`, `10s`, `2m`.

What is checked before connecting: one kind per step; `answer` only on `wait_for`; `payload` and `expect_error` not on `wait_for`; `expect`, `expect_error` and `save` not on `send`; `expect` and `expect_error` not together; no fields on `expect_no_call`, `sleep`, `disconnect`, `reconnect`; payload files readable and valid JSON; and, with `version`, that each action exists in it, that `call` and `send` actions are sent by a station and `wait_for` actions by the CSMS, and that `call` and `send` match the action's kind (a SEND needs `send`). `raw: true` relaxes the action checks of `call` and `send`.

At run time, the request of a `call` or `send` is validated against the schema of the negotiated version before it is sent (unless `raw`); an invalid one fails the step and is not sent. Every response is validated: a response that does not conform fails the step, and is shown in the result. A `call` after a `disconnect` without `reconnect` fails.

## Expectations

`expect` maps a **path** to a **condition**. Paths are dotted field names, with `[i]` (or `.i`) for list elements, 0-based:

```yaml
expect:
  status: Accepted
  idTokenInfo.status: Accepted
  getVariableResult[0].attributeStatus: Accepted
  interval: {present: true}
  currentTime: {regex: "^20[0-9]{2}-"}
  idTagInfo.status: {one_of: [Accepted, ConcurrentTx]}
  statusInfo: {present: false}
```

| Condition | Meaning |
|---|---|
| a value (string, number, bool, list, object) | equality. Values are compared as JSON, so YAML's `300` equals JSON's `300.0`; `"300"` (a string) does not equal `300` |
| `{regex: "…"}` | the field matches the Go regular expression (unanchored: use `^` and `$`). A field that is not a string is matched in its JSON form |
| `{present: true}` / `{present: false}` | the field is there, or is not |
| `{one_of: [a, b, …]}` | the field equals one of the values |

Every condition except `present: false` fails when the field is absent. All failures of a step are reported, sorted by path:

```
FAIL     1  call BootNotification (0 ms): interval is 300, want one of [60,120]
```

Variables are not substituted in `expect`.

## Variables

`${name}` in a `payload`, or in the `answer` of a `wait_for`, is replaced before the message is sent:

- A string that is only `"${name}"` becomes the variable's value with its JSON type: a saved integer stays an integer. That is how the 1.6 example sends `transactionId: "${tx}"` as the integer the CSMS assigned.
- `${name}` inside a longer string is replaced by its text: `"${identity}-tx1"`.

| Variable | Value |
|---|---|
| `${now}` | the current time, UTC, RFC 3339 with milliseconds (`2026-10-18T09:12:03.120Z`) |
| `${uuid}` | a new random UUID (version 4) at every use |
| `${identity}` | the station identity of `--url` |
| `--var NAME=VALUE` | a string |
| `save:` | the saved value, with its JSON type |

The built-in names take precedence over `--var` and `save` of the same name. A variable that is not defined when the step runs fails the step (`undefined variable ${tx}: save it in an earlier step`). Names are letters, digits and `_`, not starting with a digit. `save` runs after `expect` passed.

## Payload files

A `payload` or `answer` can be a JSON file: `"@file"`. The path is relative to the directory of the scenario file (or absolute). **Quote it:** `@` cannot start a plain YAML scalar, so `payload: @meter.json` is a YAML syntax error; write `payload: "@meter.json"`.

```yaml
  - call: MeterValues
    payload: "@../payloads/meter-1.6.json"
```

The file is read when the scenario is loaded, and variables in it are substituted when the step runs. A payload can also be a JSON string in YAML: `payload: '{"connectorId": 1}'`.

## CALLs from the CSMS

The CSMS may send CALLs at any time. How they are handled:

- **A step waits for it.** When a CALL arrives for an action that the current step or a later `wait_for` step waits for, it is held for that step: the CSMS gets no answer until the step runs, then the step's `answer`. A CALL that arrives early, while `call`, `sleep` or other steps run, is therefore not lost; keep those steps short enough for the CSMS's own response timeout.
- **Out of order.** OCPP-J lets a peer have one CALL outstanding at a time. When a `wait_for` waits for X and the CSMS sends Y, which a later step waits for (or Y is already held), X cannot arrive until Y is answered, and Y's answer belongs to its own step. The `wait_for` of X therefore fails at once, saying so ("reorder the steps"), instead of waiting for its timeout; Y stays held for its step, which can still pass with `--keep-going`.
- **Progress.** While a `wait_for` or `expect_no_call` runs, stderr says what it waits for (`waiting up to 30s for RequestStartTransaction from the CSMS`, `expecting no CALL from the CSMS for 10s`).
- **No step waits for it.** It is answered at once with the first answer its response schema accepts: `{}`, then `{"status":"Accepted"}`, else the CALLERROR `NotSupported`. It is listed in the result as **unexpected**, with the answer it got. Unexpected CALLs do not fail the scenario.
- **SENDs** from the CSMS (2.1) are listed as unexpected.
- A held CALL whose request fails the schema is answered at once with the CALLERROR of the violation and does not reach its step, which then times out.
- **`expect_no_call`** fails when any CALL or SEND arrives during it, whether a later step waits for it or not; the CALL is still handled as above.

## Result and exit codes

| Exit | When |
|---|---|
| 0 | every step passed |
| 7 | some steps failed: the result is written, with the reason of each failure |
| 3 | the scenario is invalid, a `--var` is malformed, or a flag is wrong: nothing was sent |
| 4 | the first connection failed (refused handshake, TLS, no common version) |
| 10 | interrupted (Ctrl-C) |

After the first failing step the remaining steps are **skipped** (and counted as such), unless `--keep-going`. A `reconnect` that fails is a failed step.

```console
$ ocppctl client script regression.yaml --url ws://localhost:9000/CI001 --insecure-transport --keep-going
scenario regression against ws://localhost:9000/CI001 (OCPP 2.0.1)
FAIL     1  call BootNotification (0 ms): interval is 300, want one of [60,120]
PASS     2  call Heartbeat (0 ms)
FAIL     3  call DataTransfer (0 ms): answered with a CALLRESULT, want CALLERROR NotSupported
FAIL     4  no remote start: wait_for RequestStartTransaction (1002 ms): no RequestStartTransaction from the CSMS within 1s
1 passed, 3 failed, 0 skipped in 1005 ms
```

The JSON result (`--format json`) has every step with its request, response and message, and the unexpected CALLs; see [json-output.md](json-output.md#client-script).

## The examples

`examples/scenarios` holds three scenarios. They run against any CSMS that accepts the station and the token; `ocppctl server start --insecure-transport --no-auth` is one.

### `smoke-1.6.yaml`: a charging session in OCPP 1.6

BootNotification (expects `status: Accepted` and an `interval`), StatusNotification Available, Heartbeat (expects a `currentTime` that looks like a date), Authorize of `${token}` (expects `idTagInfo.status: Accepted`), StartTransaction (saves the `transactionId` the CSMS assigns as `tx`), MeterValues from `examples/payloads/meter-1.6.json` (which uses `${tx}` and `${now}`), and StopTransaction of `${tx}`. The token comes from the command line:

```console
ocppctl client script examples/scenarios/smoke-1.6.yaml --url ws://localhost:9000/CS001 --insecure-transport --var token=04A1B2C3D4E5F6
```

### `smoke-2.0.1.yaml`: a charging session in OCPP 2.0.1

BootNotification, StatusNotification, Authorize of `${token}` (expects `idTokenInfo.status: Accepted`), and the three TransactionEvents `Started`, `Updated` (with an energy reading) and `Ended`. In 2.x the station chooses the transaction id: here `${identity}-tx1`.

```console
ocppctl client script examples/scenarios/smoke-2.0.1.yaml --url ws://localhost:9000/CS001 --insecure-transport --var token=04A1B2C3D4E5F6
```

### `remote-start-2.0.1.yaml`: the CSMS starts a session

BootNotification, then a `wait_for: RequestStartTransaction` of up to 60 s that expects an `idToken.idToken` and a `remoteStartId`, answers Accepted and saves both; then a TransactionEvent `Started` with `triggerReason: RemoteStart` that carries them back (with `transactionId: "${uuid}"`); then `expect_no_call: 2s`. Run it, and have the CSMS send the RequestStartTransaction within a minute. With ocppctl's own CSMS:

```console
ocppctl server start --insecure-transport --no-auth &
ocppctl client script examples/scenarios/remote-start-2.0.1.yaml --url ws://localhost:9000/CS001 --insecure-transport &
sleep 1
ocppctl server call RequestStartTransaction --station CS001 --yes \
  --payload '{"remoteStartId":7,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}'
wait %2
```

## In CI

A scenario per behaviour of the CSMS under test, run with `--format json --keep-going`, the result kept as an artifact:

```yaml
# .github/workflows/csms-regression.yml (excerpt)
- name: OCPP regression
  env:
    OCPPCTL_PASSWORD: ${{ secrets.CSMS_TEST_PASSWORD }}
  run: |
    mkdir -p results
    status=0
    for s in tests/ocpp/*.yaml; do
      ocppctl client script "$s" --url "wss://csms-staging.example.com/ocpp/CI001" \
        --keep-going --format json > "results/$(basename "$s" .yaml).json" || status=$?
    done
    jq -r '.steps[] | select(.result == "fail") | "\(.index) \(.action): \(.message)"' results/*.json
    exit $status
```

Exit `7` means a step failed (the JSON says which); `3` a broken scenario; `4` a CSMS that could not be reached. Give each CI job its own identity, or run the scenarios one after another: a CSMS replaces or refuses a second connection of the same identity.
