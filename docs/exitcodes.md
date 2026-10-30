# Process exit codes

`ocppctl` uses stable exit codes for scripting. A fatal error is rendered once: as one `error:` line on stderr, or, when `--format json` is active, as a single JSON error object on stdout (and nothing else on stdout).

| Code | Meaning |
|------|---------|
| 0 | Success (`ExitOK`) |
| 2 | Usage (`usage`): unknown command or flag, a missing required flag, a value a flag cannot hold; a state-changing `server call` without `--yes` |
| 3 | Invalid input (`invalid_input`): a flag value, combination of flags, environment variable, payload, model or scenario that does not make sense. Nothing was sent |
| 4 | Transport (`transport`): the connection cannot be made (refused, unreachable, TLS failure, handshake refused, no common OCPP version), is lost, the server cannot listen, or the control socket of a running server cannot be reached |
| 5 | Timeout (`timeout`): no response in time |
| 6 | Protocol (`protocol`, `call_error`): the peer answered with a CALLERROR, sent a response that does not conform to the OCA schema, violated the OCPP-J framing, or did not accept a `--boot` BootNotification |
| 7 | Partial success (`ExitPartial`): a **valid** result was written, and it holds failures: a `client script` step that failed, or a `client run` station that failed while others ran |
| 10 | Output / internal (`output`, `internal`): the result could not be written, or something unexpected happened |
| 130 | Interrupted (`interrupted`): Ctrl-C or SIGTERM before a one-shot command had its result |

**Kind vs code:** `kind` drives the exit code; `code` in the JSON error object is stable for machines. Do not parse the human `message`.

| `kind` | Exit | `code` values |
|--------|------|---------------|
| `usage` | 2 | `invalid_input` (what the command-line parser rejects: unknown flag, missing required flag such as `--station` or `--payload`), `invalid_flag_combination` (`--boot` with `client boot`), `confirmation_required` (`server call` of a state-changing action without `--yes`) |
| `invalid_input` | 3 | `invalid_input`, `invalid_format` (`--format`), `invalid_url` (`--url` missing or not `ws[s]://host/…/<identity>`, credentials in the URL, a bad `{N..M}` range), `invalid_flag_combination` (`ws://` without `--insecure-transport`, TLS flags with `ws://`, `--tls-cert` without `--tls-key`, `--payload-only` with several versions, a server without authentication or without TLS, …), `invalid_config` (a station model, CSMS model or scenario that does not validate), `invalid_payload` (the request does not conform to the OCA schema, or is not a JSON object), `unknown_action` (not an action of the version, or sent by the other role), `unsupported_version` (an unknown `--ocpp-version`, a frame type the version does not have, `client send` when the CSMS did not choose 2.1), `input_file_open_failed`, `input_decode_failed` (a payload that is not JSON, a PEM file without a certificate, a malformed line of `--passwords-file`), `output_file_create_failed` (`generate-config --output` names an existing file and `--force` is not given), `protocol_violation` (`frame decode` of a malformed frame) |
| `transport` | 4 | `transport_connect_failed`, `transport_tls_failed`, `handshake_refused` (the CSMS answered the WebSocket handshake with an HTTP error; see `ocpp.http_status`), `no_common_version` (the CSMS accepts none of the versions offered), `connection_lost`, `listen_failed`, `control_socket_failed`, `station_not_connected` |
| `timeout` | 5 | `request_timeout` (no response to a request that changes nothing), `outcome_unknown` (no response to a request that may have changed state: it was **not** sent again) |
| `call_error` | 6 | `call_error` (the peer answered with a CALLERROR; see `ocpp`), `boot_not_accepted` (the BootNotification of `--boot` was answered Pending or Rejected) |
| `protocol` | 6 | `invalid_response` (the response does not conform to the OCA schema), `protocol_violation` (the peer broke the OCPP-J framing) |
| `output` | 10 | `json_encode_failed`, `table_render_failed`, `output_write_failed`, `output_file_write_failed`, `output_file_create_failed` |
| `internal` | 10 | `internal_unexpected` |
| `interrupted` | 130 | `context_canceled` (interrupted with Ctrl-C before a result) |

`kind` is `call_error` for a refusal by the peer and `protocol` for a peer that broke the rules; both exit 6.

## The error object

```json
{
  "error": {
    "kind": "call_error",
    "code": "call_error",
    "message": "Heartbeat: the peer answered with CALLERROR FormatViolation: bogus: not a property of this object"
  },
  "ocpp": {
    "error_code": "FormatViolation",
    "description": "bogus: not a property of this object"
  }
}
```

`ocpp` carries protocol detail and is omitted when there is none. Its shape depends on the error:

| Error | `ocpp` fields |
|-------|---------------|
| `call_error` | `error_code` (the CALLERROR code, such as `NotSupported`, `FormatViolation`), `description`, `details` (the CALLERROR's details object; omitted when it is empty) |
| `invalid_payload`, `invalid_response` | `field` (the path of the offending field; omitted for the payload as a whole), `reason`, `error_code` (the CALLERROR code a peer would answer with: `FormationViolation`, `OccurenceConstraintViolation`, … in 1.6; `FormatViolation`, `OccurrenceConstraintViolation`, … in 2.x) |
| `handshake_refused` | `http_status`, `meaning` (what that status usually means: 401 wrong or missing password, 403 refused station or security profile, 404 unknown identity or wrong path, 409 already connected, 400 invalid identity, 503 full or shutting down) |
| `boot_not_accepted` | `status` (`Pending` or `Rejected`) |

```json
{
  "error": {
    "kind": "transport",
    "code": "handshake_refused",
    "message": "connection refused by the CSMS: HTTP 401: authentication failed: wrong password, or no credentials (security profiles 1 and 2)"
  },
  "ocpp": {
    "http_status": 401,
    "meaning": "authentication failed: wrong password, or no credentials (security profiles 1 and 2)"
  }
}
```

```json
{
  "error": {
    "kind": "invalid_input",
    "code": "invalid_payload",
    "message": "BootNotification: the request does not conform to the OCA schema: ocpp: invalid message: chargingStation: required (nothing was sent)"
  },
  "ocpp": {
    "field": "chargingStation",
    "reason": "required",
    "error_code": "OccurrenceConstraintViolation"
  }
}
```

Errors the command-line parser finds (an unknown flag, a missing required flag) are found before `--format` is read: they are always one `error:` line on stderr, exit 2. The same holds for `--boot` given to `client boot`.

## Partial exit

`7` only after a success payload was written. A failure to connect, to validate or to write is fatal and uses the codes above, never `7`.

- **`client script`**: the result lists every step with `pass`, `fail` or `skipped`. When at least one step failed, the result is written and the exit code is `7`. A scenario that does not validate exits `3` before connecting; a first connection that fails exits with its transport code (`4`). A failing `reconnect` step is a failed step, not a fatal error.
- **`client run`** with an identity range (`{001..100}`): when some stations failed (their first connection was refused, or the connection was lost with `--no-reconnect`) and others ran, the exit code is `7`, and the failed identities are listed on stderr. When every station failed, the error of the first is fatal. A single station whose first connection fails exits `4` at once.

## Streaming commands

`client run` and `server start` write one record per event. Stopping them with Ctrl-C (SIGINT) or SIGTERM, or the end of `--duration`, ends the stream normally, also while a station is still connecting: exit `0` (or `7` for `client run` as above). `client run --no-reconnect` exits `4` (`connection_lost`) when the connection of its only station is lost. `server start` exits `4` when it cannot listen or create its control socket.

## `validate`

An invalid payload is a result, not an error: `validate` writes the result (with `valid: false` and the violations) to stdout and exits `3`. An unknown action or version is a fatal error (exit `3`, with the error object instead of a result).

## State-changing CALLs and `--yes`

- **`client call`** and the shortcuts validate the request before sending it; an invalid one exits `3` and nothing was sent. A CALL is sent once. When no response arrives in `--timeout`, a request that may change state at the CSMS exits `5` with `outcome_unknown`: the CSMS may have acted on it, so do not blindly resend it. Only Heartbeat, Authorize and the read-only `Get…` requests (GetCertificateStatus, Get15118EVCertificate, …) exit with `request_timeout`.
- **`server call`** refuses a CALL that may change the station without `--yes` (exit `2`, `confirmation_required`): everything but the read-only requests (GetConfiguration, GetVariables, GetBaseReport, GetReport, GetMonitoringReport, GetLocalListVersion, GetCompositeSchedule, GetChargingProfiles, GetDisplayMessages, GetInstalledCertificateIds, GetTransactionStatus, GetTariffs, GetDERControl, GetPeriodicEventStream). GetLog and GetDiagnostics make a station upload files, and need `--yes`. A CALL that times out exits `5`, `outcome_unknown` for a state-changing one. A station that is not connected is `station_not_connected` (exit `4`): nothing was sent, and trying again once it is connected is safe.
- **`client send`** (OCPP 2.1 SEND) exits `5` with `outcome_unknown` when the SEND was queued but not written within `--timeout`.

## `--debug`

`--debug` adds the frame trace, go-ocpp's log and, after a fatal error, the chain of causes (`cause: …` lines) on stderr. It does not change stdout or the exit code.

**Organisation table:** these codes follow the OT Fabric CLI exit-code table. ocppctl does not use `1`, `8` and `9`: they keep their organisation meaning and are not reused for anything else.

**Format variable:** `OCPPCTL_FORMAT` is the name to use; `OCPPCTL_OUTPUT_FORMAT` stays accepted. When both are set, `OCPPCTL_FORMAT` wins.
