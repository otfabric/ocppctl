# `--format json` field conventions

These conventions are a **stable contract** for scripting. A breaking change to a name or shape is called out in the release notes.

## General

| Concept | JSON fields |
|--------|-------------|
| Names | snake_case for everything ocppctl adds (`duration_ms`, `error_code`) |
| OCPP payloads | `request`, `response`, `payload`: the OCPP JSON **as on the wire**, camelCase, unchanged (`{"idTagInfo":{"status":"Accepted"}}`) |
| Target | `target`: the URL the station connected to (`wss://csms.example.com/ocpp/CS001`); for `server call` the station identity, for `server stations` the control socket |
| Version | `version`: `"1.6"`, `"2.0.1"` or `"2.1"`: the version negotiated, or the one given |
| Times | RFC 3339 in UTC with exactly three fractional digits (`2026-10-18T09:12:03.120Z`): event `time`, `received` / `unexpected` entries, `connected_at`, and the times ocppctl puts into payloads (`timestamp`, `currentTime`). Times inside a peer's payload are as the peer wrote them |
| Durations | `duration_ms`, `handshake_ms`: integers, milliseconds |
| Lists | `received`, `attempts`, `accepted`, `actions`, `violations`, `steps`, `unexpected`, `stations`: **arrays**, empty arrays rather than null |
| Fatal error | one object `{"error": {"kind", "code", "message"}, "ocpp": {…}}` on stdout, nothing else; see [exitcodes.md](exitcodes.md) |

Fields that do not apply are omitted, not null, unless the tables below say otherwise.

## Results

### `client call`, the shortcuts, `server call`

`client call`, `client boot`, `heartbeat`, `authorize`, `status`, `meter`, `data-transfer`, `transaction start|stop`, and `server call` all print a **CallResult**:

| Field | Meaning |
|-------|---------|
| `target` | the CSMS URL (`client`), or the station identity (`server call`) |
| `version` | the version the session uses |
| `action` | the action sent |
| `request` | the payload sent (for a shortcut: the one it built) |
| `response` | the CALLRESULT payload |
| `duration_ms` | from sending the CALL to the response |
| `validated` | `true` when the request was checked against the schema before it was sent; `false` with `--raw`. The response is always checked |
| `received` | CALLs and SENDs the peer sent meanwhile, and how they were answered: `time`, `action`, `request`, `response` (omitted for a SEND, or when answered with a CALLERROR), `error_code` (the CALLERROR code, when answered with one). Always `[]` for `server call` |

```json
{
  "target": "ws://127.0.0.1:9000/CS001",
  "version": "2.0.1",
  "action": "BootNotification",
  "request": {"chargingStation": {"model": "ocppctl", "vendorName": "OTFabric"}, "reason": "PowerUp"},
  "response": {"currentTime": "2026-10-18T20:48:54.125Z", "interval": 300, "status": "Accepted"},
  "duration_ms": 0,
  "validated": true,
  "received": []
}
```

A shortcut or call succeeds whenever a valid CALLRESULT arrives, whatever status it carries: check `.response.status`, `.response.idTagInfo.status` and so on yourself.

### `--payload-only`

| Field | Meaning |
|-------|---------|
| `version` | the one `--ocpp-version` given |
| `action` | the action the shortcut would send |
| `payload` | the request payload, validated |

With `--format text` only the payload itself is printed, so that it can be piped into `--payload -`.

### `client send`

| Field | Meaning |
|-------|---------|
| `target`, `version`, `action`, `request` | as above |
| `sent` | `true`: the SEND was written. A SEND is never answered |

### `client probe`

| Field | Meaning |
|-------|---------|
| `target`, `identity` | the URL and the identity (its last path segment) |
| `attempts` | one per connection: one, or one per version with `--each-version` |
| `attempts[].offered` | the versions offered in this attempt |
| `attempts[].ok` | whether the handshake completed with an OCPP version |
| `attempts[].negotiated` | the version the CSMS chose (omitted when refused) |
| `attempts[].http_status` | the HTTP status of the handshake: `101` when accepted, the refusal's status otherwise (omitted when there was none, such as a connection error) |
| `attempts[].compression` | permessage-deflate negotiated |
| `attempts[].handshake_ms` | the time of the HTTP upgrade |
| `attempts[].authentication` | what ocppctl offered: `none`, `basic`, `certificate` or `basic+certificate` |
| `attempts[].tls` | for `wss://`: `version`, `cipher_suite`, `server_name`, `subject`, `issuer`, `not_after` (of the CSMS certificate), `client_certificate` (whether one was configured) |
| `attempts[].error` | `kind`, `code`, `message` of a refused attempt |
| `accepted` | the versions the CSMS accepted |

`probe` fails (exit 4, the error object instead of this result) only when no attempt was accepted.

### `client script`

| Field | Meaning |
|-------|---------|
| `scenario` | the scenario's `name`, or its file name |
| `target`, `version` | the URL and the version negotiated |
| `passed`, `failed`, `skipped` | step counts |
| `steps` | one per step: `index` (1-based), `name`, `kind` (`call`, `send`, `wait_for`, `expect_no_call`, `sleep`, `disconnect`, `reconnect`), `action`, `result` (`pass`, `fail`, `skipped`), `duration_ms`, `message` (why it failed), `request` (sent by a `call`/`send`, or received by a `wait_for`), `response` (received, or answered by a `wait_for`), `error_code` (a CALLERROR received or answered) |
| `unexpected` | CALLs and SENDs from the CSMS that no step waited for, as in `received` above |
| `duration_ms` | the whole run |

### `actions`

`version`, and `actions`: `name`, `from` (`station`, `csms`, or `both` for DataTransfer), `kind` (`call`, or `send` for the OCPP 2.1 messages that are never answered).

### `validate`

| Field | Meaning |
|-------|---------|
| `version`, `action` | as given |
| `message` | `"request"` or `"response"`: what was validated |
| `valid` | the result |
| `violations` | `field` (omitted for the payload as a whole), `reason`, `error_code` (the CALLERROR code a peer would answer with) |

An invalid payload is written as a result and exits 3.

### `frame decode`, `frame encode`

| Field | Meaning |
|-------|---------|
| `version` | the version whose framing rules applied |
| `type`, `type_number` | `CALL` 2, `CALLRESULT` 3, `CALLERROR` 4, `CALLRESULTERROR` 5, `SEND` 6 |
| `message_id` | the message id |
| `action` | of a CALL or SEND |
| `payload` | of a CALL, CALLRESULT or SEND |
| `error_code`, `description`, `details` | of a CALLERROR or CALLRESULTERROR |
| `frame` | the frame as one line of JSON text |
| `payload_valid`, `violation` | `frame decode` of a CALL or SEND, unless `--no-validate`: whether the payload conforms to its schema, and why not (`field`, `reason`, `error_code`) |

### `cert hash`

| Field | Meaning |
|-------|---------|
| `certificate` | the subject of the certificate |
| `hash_algorithm`, `issuer_name_hash`, `issuer_key_hash`, `serial_number` | CertificateHashData, hex |
| `ocpp` | the same as OCPP messages carry it: `{"hashAlgorithm", "issuerNameHash", "issuerKeyHash", "serialNumber"}`, ready for a `certificateHashData` field |

### `server stations`

`target` (the control socket) and `stations`: `identity`, `version`, `remote`, `connected_at`, `tls`, `compression`, `session_id`.

## Streams

### `client run`: station events

One object per line (JSON Lines), written as it happens.

| Field | Meaning |
|-------|---------|
| `time` | when it happened (UTC) |
| `station` | the identity |
| `event` | see below |
| `version` | the session's version (message events, `connected`) |
| `action`, `request`, `response` | the message |
| `error_code` | a CALLERROR received (`call_error`) or answered (`answered`); for a `call_error` without CALLERROR, the ocppctl error code (`request_timeout`, `invalid_response`, …) |
| `duration_ms` | `call_result`, `call_error` |
| `transaction` | `session_started`, `session_ended`: the transaction id (1.6: the integer the CSMS gave, as a string) |
| `message` | human detail: `tls=… compression=…`, the heartbeat interval, the session, an error text |

| `event` | When |
|---------|------|
| `connected` | a connection was made (also after a Reset) |
| `booted` | a BootNotification was Accepted |
| `boot_pending`, `boot_rejected` | a BootNotification was answered Pending or Rejected; `message` says when it is retried |
| `call_sent` | the station sent a CALL |
| `call_result` | its valid CALLRESULT arrived |
| `call_error` | it failed: a CALLERROR, an invalid response, a timeout or a lost connection |
| `call_received` | the CSMS sent a CALL |
| `answered` | it was answered: `response`, or `error_code` |
| `send_received` | the CSMS sent a SEND (OCPP 2.1) |
| `session_started`, `session_ended` | a simulated charging session started or ended |
| `reset` | a Reset was accepted: sessions end, the station reconnects and boots again |
| `disconnected` | the connection was lost |
| `reconnecting` | the station reconnects |
| `error` | something went wrong in the station (a connection that failed for good, a session that could not start, an answer that was not valid) |

The summary of a run with several stations goes to stderr as text.

### `server start`: server events

One object per line, written as it happens; every event has `time` (UTC, three fractional digits) and `event`.

| `event` | Fields |
|---------|--------|
| `listening` | `address`, `versions`, `control_socket` (when enabled) |
| `connected` | `identity`, `version`, `remote`, `tls`, `compression`, `session_id` |
| `refused` | `identity` (when known), `remote`, `reason` |
| `call_received` | `identity`, `version`, `action`, `request` |
| `answered` | `identity`, `version`, `action`, and `response` or `error_code` (+ `error_description`) |
| `send_received` | `identity`, `version`, `action`, `request`; `error_code`, `error_description` when it fails the schema |
| `call_sent` | `identity`, `version`, `action`, `request` (a `server call`) |
| `call_result` | `identity`, `version`, `action`, `duration_ms`, and `response`, or `error_code` / `error_description` (CALLERROR), or `reason` (timeout, lost connection, invalid response) |
| `disconnected` | `identity`, `version`, `remote`, `session_id`, `reason` |
| `stopping` | |

See [server-config.md](server-config.md#events).

## Streams and stderr

- A streaming command writes **compact** JSON, one object per line; every other command writes **one** indented JSON document.
- Progress (`connected to … as … (OCPP 2.1)`, the step lines of `client script`), summaries and the `--debug` trace go to **stderr**, as text, in every format. stdout holds nothing but the result.
- `client generate-config` and `server generate-config` write a model, not a result: their `--format` is the format of the model (`yaml` or `json`).
