# The CSMS: `ocppctl server`

`ocppctl server start` is a CSMS (in OCPP 1.6: a Central System) that charging stations
connect to. It authenticates them, answers their CALLs from a model, and streams what
happens as events. While it runs, `server stations` lists the connected stations and
`server call` sends one of them a CALL, through the server's control socket.

- [Starting a server](#starting-a-server)
- [Authentication and security profiles](#authentication-and-security-profiles)
- [The model](#the-model)
- [Events](#events)
- [The control socket](#the-control-socket)
- [`server call` and `server stations`](#server-call-and-server-stations)
- [Runbook](#runbook)

OCPP controls real chargers. `server call` can reset a station or start a charging
session: point stations at an ocppctl CSMS only when you are allowed to.

## Starting a server

```
ocppctl server start [--config csms.yaml] [flags]
```

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `--listen` | `OCPPCTL_LISTEN` | `:9000` | host:port to listen on |
| `--path` | `OCPPCTL_PATH` | `/` | URL path prefix; stations connect to `<path><identity>`. Any other path gets HTTP 404 (and a `refused` event) |
| `--ocpp-version` | `OCPPCTL_OCPP_VERSION` | `2.1,2.0.1,1.6` | versions accepted; a station gets the first of its offered subprotocols the server accepts |
| `--config` | `OCPPCTL_CONFIG` | built-in | the model file, YAML or JSON |
| `--passwords-file` | `OCPPCTL_PASSWORDS_FILE` | | `identity:password` lines (see below) |
| `--no-auth` | `OCPPCTL_NO_AUTH` | false | accept every station without credentials |
| `--tls-cert`, `--tls-key` | `OCPPCTL_TLS_CERT`, `OCPPCTL_TLS_KEY` | | listen with TLS (wss://) |
| `--tls-client-ca` | `OCPPCTL_TLS_CLIENT_CA` | | verify station client certificates against these CAs (security profile 3) |
| `--insecure-transport` | `OCPPCTL_INSECURE_TRANSPORT` | false | allow listening without TLS (ws://) |
| `--control-socket` | `OCPPCTL_CONTROL_SOCKET` | `ocppctl.sock` | the control socket; an empty value disables it |
| `--max-stations` | `OCPPCTL_MAX_STATIONS` | 0 (10000) | more simultaneous stations are refused with HTTP 503 |
| `--ping-interval` | `OCPPCTL_PING_INTERVAL` | 0 | send WebSocket pings at this interval and drop a station whose pong is late |
| `--rate-limit`, `--rate-burst` | `OCPPCTL_RATE_LIMIT`, `OCPPCTL_RATE_BURST` | 0 | handle at most this many messages a second per station (more wait; OCPP has no "too many" error); the burst defaults to the rate, rounded up |
| `--allow` | `OCPPCTL_ALLOW` | any | accept connections only from these addresses or CIDR prefixes (others: HTTP 403) |
| `--duration` | `OCPPCTL_DURATION` | until Ctrl-C | stop after this long, e.g. `10m` |
| `--format` | `OCPPCTL_OUTPUT_FORMAT` | `text` | events as text, json (JSON Lines) or table |
| `--debug` | | false | every frame and go-ocpp's log on stderr (never credentials) |

Everything is checked before the server listens: a bad flag combination, an unreadable
certificate or an invalid model exits 3 with every problem of the model listed. A port
that cannot be bound or a control socket that cannot be created exits 4. Ctrl-C or the
end of `--duration` stops the server (exit 0): every session is closed with WebSocket
status 1001 and the control socket is removed.

The station identity is the last path segment of the URL. OCPP 2.x identities must be
identifierStrings without `:`, at most 48 characters (go-ocpp refuses others with HTTP 400). When a station
connects again with the same identity, the new connection replaces the old one.

## Authentication and security profiles

A server needs a way to authenticate stations, or `--no-auth` explicitly.

| Profile | Transport | Station credentials | Flags |
|---|---|---|---|
| 0 | ws:// | none | `--insecure-transport --no-auth` |
| 1 | ws:// | HTTP Basic password | `--insecure-transport`, passwords |
| 2 | wss:// | HTTP Basic password | `--tls-cert --tls-key`, passwords |
| 3 | wss:// | client certificate | `--tls-cert --tls-key --tls-client-ca` |

- **Passwords** come from the model (`stations[].password`) and `--passwords-file`
  (which wins for an identity in both). They are never accepted on the command line, and
  never printed. The Basic user name must be the identity. Once any password is
  configured, a station whose identity has none is refused with HTTP 404 (unknown
  station); a wrong or missing password gets HTTP 401.
- **Passwords file**: one `identity:password` per line, split at the first `:`; blank
  lines and lines starting with `#` are skipped. A malformed line is reported by number,
  never by content.
- **Client certificates** (`--tls-client-ca`): the station's certificate must chain to
  one of the CAs, and its subject CommonName must be the identity. Profiles 2 and 3 can
  be combined: with passwords as well, a certificate is verified when one is sent and a
  station without one authenticates with its password.
- Every refused connection is reported as a `refused` event with the identity, the
  remote address and the reason (wrong path, unknown identity, no credentials,
  authentication failed, address not allowed). Credentials never appear in events or logs.
- Without TLS ocppctl refuses to listen unless `--insecure-transport` is given, the
  same rule as for the client.

## The model

The model decides what OCPP leaves to the CSMS: whether a station is accepted, which
tokens are valid, which transaction ids are given. `ocppctl server generate-config`
writes the built-in model with comments; `--format json` writes it as JSON. Decoding is
strict: a field ocppctl does not know is an error.

```yaml
boot:
  status: Accepted          # Accepted (default), Pending or Rejected
  interval: 300             # seconds (default 300)
stations:
  - identity: CS001
    password: change-me     # HTTP Basic password (profiles 1 and 2)
  - identity: CS002
    boot_status: Pending    # overrides boot.status
authorization:
  tokens: [04A2B3C4D5E6F7, TESTTOKEN]   # empty: every token is accepted
transactions:
  first_id: 1               # first OCPP 1.6 transactionId (default 1)
actions:
  - action: DataTransfer
    versions: ["1.6"]
    stations: [CS001]
    response: {status: Accepted, data: hello}
  - action: SignCertificate
    answer: not-supported
```

| Field | Type | Meaning |
|---|---|---|
| `boot.status` | string | the `status` of every BootNotification answer: `Accepted`, `Pending` or `Rejected` |
| `boot.interval` | integer ≥ 0 | the `interval` of the answer in seconds: the heartbeat interval when Accepted, the retry interval otherwise |
| `stations[].identity` | string, required, unique | the station identity |
| `stations[].password` | string | its HTTP Basic password |
| `stations[].boot_status` | string | overrides `boot.status` for this station |
| `authorization.tokens` | list of strings | the accepted idTag (1.6) or idToken.idToken (2.x) values; any other is answered `Invalid`. Empty accepts all |
| `transactions.first_id` | integer ≥ 1 | the transactionId of the first 1.6 StartTransaction; each further one gets the next integer (shared by all stations, not persisted) |
| `actions[].action` | string, required | the action the override answers |
| `actions[].versions` | list | the versions it applies to (default: all served) |
| `actions[].stations` | list | the identities it applies to (default: all) |
| `actions[].answer` | string | `response` (default), `reject` (the payload `{"status":"Rejected"}`), or `not-supported` (the CALLERROR `NotSupported`) |
| `actions[].response` | object | the CALLRESULT payload for `answer: response` |

Every override is checked when the model loads: the action must be one a station sends
in the versions it applies to (not a CSMS action, not a SEND), and its payload must
pass the OCA response schema of each of those versions. `reject` is therefore only
possible for actions whose response is a bare `status` (DataTransfer, for one). The
first override that matches a CALL (action, version, station) applies.

### Built-in answers

Every incoming CALL is first validated against the OCA request schema of the session's
version; an invalid one is answered with the CALLERROR of the violation
(FormationViolation/FormatViolation, OccurrenceConstraintViolation, ...). Then:

| Action | Answer |
|---|---|
| BootNotification | `status` from the model, `currentTime` now, `interval` from the model |
| Heartbeat | `currentTime` now |
| Authorize | 1.6 `idTagInfo.status`, 2.x `idTokenInfo.status`: `Accepted` or `Invalid` from the token list |
| StartTransaction (1.6) | the next `transactionId`, `idTagInfo` from the token list |
| StopTransaction (1.6) | `idTagInfo` from the token list when the request has an `idTag`, otherwise `{}` |
| TransactionEvent (2.x) | `idTokenInfo` from the token list when the request has an `idToken`, otherwise `{}` |
| DataTransfer | `status: UnknownVendorId`: OCPP requires it of a recipient without an implementation for the vendorId, and ocppctl implements none |
| anything else | the first of `{}` and `{"status":"Accepted"}` its response schema accepts (StatusNotification, MeterValues, NotifyReport, ...), or the CALLERROR `NotSupported` (Get15118EVCertificate, GetCertificateChainStatus and VatNumberValidation, for which ocppctl has no PKI or VAT service, unless the model answers them) |

Every answer, built-in or from the model, is validated against the response schema
before it is sent. Times are UTC RFC 3339 with milliseconds. SENDs (OCPP 2.1) are never
answered; they are reported as `send_received`.

## Events

`server start` writes one record per event to stdout: a line with `--format text`, one
JSON object per line with `--format json`, a table row with `--format table`. Progress
and `--debug` output go to stderr.

| Event | When | Fields |
|---|---|---|
| `listening` | the server is ready | `address`, `versions`, `control_socket` (when enabled) |
| `connected` | a station's session started | `identity`, `version`, `remote`, `tls`, `compression`, `session_id` |
| `refused` | a connection was refused before the WebSocket upgrade | `identity` (when known), `remote`, `reason` |
| `call_received` | a CALL from a station arrived | `identity`, `version`, `action`, `request` |
| `answered` | it was answered | `identity`, `version`, `action`, and `response` or `error_code` (+ `error_description`) |
| `send_received` | a SEND (2.1) arrived | `identity`, `version`, `action`, `request`; `error_code` when it fails the schema |
| `call_sent` | `server call` sent a CALL | `identity`, `version`, `action`, `request` |
| `call_result` | its outcome | `identity`, `version`, `action`, `duration_ms`, and `response`, or `error_code`/`error_description` (CALLERROR), or `reason` (timeout, lost connection, invalid response) |
| `disconnected` | a session ended | `identity`, `version`, `remote`, `session_id`, `reason` |
| `stopping` | the server stops | |

Every event has `time` (UTC, RFC 3339 with milliseconds) and `event`; fields that do not
apply are omitted. `request` and `response` are the OCPP JSON as on the wire.

```json
{"time":"2026-10-18T09:12:03.120Z","event":"connected","identity":"CS001","version":"2.0.1","remote":"10.0.0.7:51544","tls":true,"compression":false,"session_id":"…"}
{"time":"2026-10-18T09:12:03.141Z","event":"call_received","identity":"CS001","version":"2.0.1","action":"BootNotification","request":{"reason":"PowerUp","chargingStation":{"model":"X","vendorName":"Y"}}}
{"time":"2026-10-18T09:12:03.142Z","event":"answered","identity":"CS001","version":"2.0.1","action":"BootNotification","response":{"currentTime":"2026-10-18T09:12:03.142Z","interval":300,"status":"Accepted"}}
```

## The control socket

`server start` creates a Unix socket that `server call` and `server stations` use to
reach it. **Default: `ocppctl.sock` in the current directory**, so that commands run
from the same directory find each other; set `--control-socket PATH` or
`OCPPCTL_CONTROL_SOCKET` on both sides otherwise (and keep the path short: Unix socket
paths are limited to about 104 bytes on macOS). A fixed path in the working directory
was chosen over a per-process temporary name, which the other commands could not
discover, and over a shared directory such as /tmp, where another user could claim the
name first.

- The socket is created with mode 0600: only its owner can send CALLs through it.
- A socket left behind by a server that is gone is replaced; if a running server answers
  on it, or the path is not a socket, `server start` exits 4 (`control_socket_failed`).
- It is removed when the server stops. `--control-socket ''` disables it.

### Protocol

One JSON object per line in each direction; every request gets exactly one response
line; a connection may carry any number of requests, one at a time.

Requests:

```json
{"op":"stations"}
{"op":"call","station":"CS001","action":"Reset","payload":{"type":"Immediate"},"raw":false,"timeout_ms":30000}
```

| Field | Meaning |
|---|---|
| `op` | `stations` or `call` |
| `station` | identity of a connected station (`call`) |
| `action` | the action to send (`call`) |
| `payload` | the request payload, a JSON object (default `{}`) |
| `raw` | skip the direction and schema checks of the request |
| `timeout_ms` | how long to wait for the response (default 30000) |

Responses:

```json
{"ok":true,"stations":[{"identity":"CS001","version":"2.0.1","remote":"10.0.0.7:51544","connected_at":"…","tls":true,"compression":false,"session_id":"…"}]}
{"ok":true,"result":{"target":"CS001","version":"2.0.1","action":"Reset","request":{…},"response":{"status":"Accepted"},"duration_ms":12,"validated":true,"received":[]}}
{"ok":false,"error":{"kind":"transport","code":"station_not_connected","message":"…"}}
{"ok":false,"error":{"kind":"call_error","code":"call_error","message":"…"},"ocpp":{"error_code":"InternalError","description":"…"}}
```

`error` and `ocpp` are those of the [JSON error envelope](exitcodes.md#the-error-object):
`server call` exits with the code of `error.kind`. Note that `server call` checks `--yes`
itself; the socket does not ask for confirmation, which is why it is private to its
owner.

## `server call` and `server stations`

```
ocppctl server call <Action> --station ID [--payload JSON|@file|-] [--raw] [--yes] [--timeout 30s]
ocppctl server stations
```

`server call` prints the result like `client call` (`target` is the station identity).
The action must be one the CSMS sends in the station's version, and the payload must
pass its schema unless `--raw`; the response is always validated.

A CALL that may change the station needs `--yes`: everything but the read-only requests
GetConfiguration, GetVariables, GetBaseReport, GetReport, GetMonitoringReport,
GetLocalListVersion, GetCompositeSchedule, GetChargingProfiles, GetDisplayMessages,
GetInstalledCertificateIds, GetTransactionStatus, GetTariffs, GetDERControl and
GetPeriodicEventStream. GetLog and GetDiagnostics make the station upload files and need
`--yes` too.

| Exit | Code | When |
|---|---|---|
| 0 | | the station answered with a valid CALLRESULT |
| 2 | `confirmation_required` | a state-changing CALL without `--yes`; or a usage error |
| 3 | `unknown_action`, `invalid_payload` | not a CSMS action of the station's version, or the payload fails the schema; nothing was sent |
| 4 | `station_not_connected` | no session with that identity |
| 4 | `control_socket_failed` | no server on the control socket |
| 4 | `connection_lost` | the station disconnected before answering |
| 5 | `outcome_unknown` | no response in `--timeout` to a state-changing CALL: the station may have acted on it, do not blindly resend |
| 5 | `request_timeout` | no response to a read-only CALL |
| 6 | `call_error` | the station answered with a CALLERROR (details in `ocpp`) |
| 6 | `invalid_response` | the response fails the schema |

`station_not_connected` is a transport failure (exit 4): like a refused connection, the
message did not reach the station, and trying again once it has connected is safe.

## Runbook

**A local CSMS for station testing** (profile 0, every version):

```sh
ocppctl server start --insecure-transport --no-auth
# point the station (or "ocppctl client probe") at ws://<host>:9000/<identity>
ocppctl client probe --url ws://localhost:9000/CS001 --insecure-transport
```

**Your own answers**:

```sh
ocppctl server generate-config --output csms.yaml
$EDITOR csms.yaml
ocppctl server start --config csms.yaml --insecure-transport --no-auth
```

**Profile 1 or 2 with passwords**:

```sh
printf 'CS001:%s\n' "$(openssl rand -hex 16)" > passwords.txt; chmod 600 passwords.txt
ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key --passwords-file passwords.txt
```

**Profile 3 with client certificates** (identity = certificate CN), and profile 2 next
to it:

```sh
ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key \
  --tls-client-ca station-ca.pem [--passwords-file passwords.txt]
```

**Send a CALL to a connected station**:

```sh
ocppctl server stations --format table
ocppctl server call Reset --station CS001 --yes --payload '{"type":"Immediate"}'            # 2.x
ocppctl server call Reset --station CP16 --yes --payload '{"type":"Soft"}'                  # 1.6
ocppctl server call RequestStartTransaction --station CS001 --yes \
  --payload '{"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}'
ocppctl server call RemoteStartTransaction --station CP16 --yes --payload '{"idTag":"TESTTOKEN","connectorId":1}'
ocppctl server call GetVariables --station CS001 \
  --payload '{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}'
```

**Pipe events to jq**:

```sh
# Every CALL a station sends, compactly
ocppctl server start --insecure-transport --no-auth --format json |
  jq -c 'select(.event=="call_received") | {identity, action, request}'

# Transactions as they start and end
ocppctl server start --config csms.yaml --insecure-transport --no-auth --format json |
  jq -c 'select(.event=="answered" and (.action=="StartTransaction" or .action=="TransactionEvent"))'

# Connections and refusals only
ocppctl server start --tls-cert s.pem --tls-key s.key --passwords-file pw.txt --format json |
  jq -c 'select(.event=="connected" or .event=="refused" or .event=="disconnected")'
```

**In CI**: start the server for a bounded time in the background, drive the station,
then inspect the events:

```sh
ocppctl server start --insecure-transport --no-auth --duration 2m --format json > events.jsonl &
./run-station-tests.sh
wait
jq -e 'select(.event=="answered" and .action=="BootNotification")' events.jsonl
```
