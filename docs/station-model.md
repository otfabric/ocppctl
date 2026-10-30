# The simulated station: `ocppctl client run`

`ocppctl client run` connects a simulated Charging Station to a CSMS and keeps it running: it boots, reports its connectors, sends heartbeats, charges the sessions its model describes, and answers what the CSMS sends it. The station is described by a **model**, a YAML or JSON file given with `--config`; without it the built-in model is used, which `ocppctl client generate-config` writes with comments.

- [Running a station](#running-a-station)
- [The model](#the-model)
- [What the station does](#what-the-station-does)
- [How the station answers the CSMS](#how-the-station-answers-the-csms)
- [Built-in behaviours](#built-in-behaviours)
- [Connections and reconnection](#connections-and-reconnection)
- [Many stations](#many-stations)
- [Events](#events)

OCPP controls real chargers, and a CSMS treats a simulated station like a real one: its sessions may be billed, and its identity may belong to a real station. Run simulated stations against a CSMS only when you are allowed to, under identities meant for testing.

## Running a station

```console
# The built-in station against a local CSMS, events as JSON Lines
ocppctl client run --url ws://localhost:9000/CS001 --insecure-transport --format json

# Your own model, OCPP 2.0.1 only, security profile 2, for ten minutes
OCPPCTL_PASSWORD=s3cret ocppctl client run --url wss://csms.example.com/ocpp/CS001 \
  --ocpp-version 2.0.1 --config station.yaml --duration 10m

# Write the built-in model to start from
ocppctl client generate-config --output station.yaml
```

Besides the connection flags every `client` command has (`--url`, `--ocpp-version`, `--password-file`, `--tls-*`, `--insecure-transport`, `--compression`, `--header`, `--timeout`, `--connect-timeout`; see the [README](../README.md#connecting-to-a-csms)):

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `--config` | `OCPPCTL_CONFIG` | built-in model | the station model, YAML or JSON |
| `--duration` | `OCPPCTL_DURATION` | `0` | stop after this long (`0`: until Ctrl-C) |
| `--no-reconnect` | | false | end with exit 4 when the connection is lost, instead of reconnecting |
| `--ping-interval` | `OCPPCTL_PING_INTERVAL` | `0` | send a WebSocket ping at this interval and treat the connection as lost when the pong is late (`0`: no pings) |
| `--stagger` | | `0` | with an identity range: delay between the start of one station and the next |
| `--format` | `OCPPCTL_FORMAT` | `text` | events as text, JSON Lines or table rows |

`--timeout` is the response timeout of every CALL the station sends. With `--debug` every frame and go-ocpp's log go to stderr.

The model is read and checked, every problem at once, before the station connects: an invalid model exits 3. The first connection must succeed: a refused or failing one exits 4. Ctrl-C or the end of `--duration` while the station is still connecting is a normal end (exit 0).

## The model

```yaml
# What the station says about itself in its BootNotification.
station:
  vendor: OTFabric
  model: Simulator
  serial: SIM-0001
  firmware: 1.0.0

# EVSEs and their connectors. OCPP 1.6 has no EVSEs: its connectors are numbered across
# the station in this order (here 1 and 2).
evses:
  - id: 1
    connectors: [1]
  - id: 2
    connectors: [1]

# heartbeat_interval: 300s
meter_interval: 60s
remote_power_w: 11000

sessions:
  - after: 30s
    evse: 1
    connector: 1
    id_token: 04A1B2C3D4E5F6
    token_type: ISO14443        # OCPP 2.x
    power_w: 11000
    meter_interval: 30s
    duration: 5m

device_model:
  OCPPCommCtrlr.HeartbeatInterval: {value: "300", mutability: ReadWrite}
  OCPPCommCtrlr.NetworkProfileConnectionAttempts: {value: "3", mutability: ReadWrite}
  AuthCtrlr.AuthorizeRemoteStart: {value: "false", mutability: ReadWrite}
  SecurityCtrlr.SecurityProfile: {value: "1", mutability: ReadOnly}
  ChargingStation.Model: {value: "Simulator", mutability: ReadOnly}

configuration:
  HeartbeatInterval: {value: "300"}
  MeterValueSampleInterval: {value: "60"}
  NumberOfConnectors: {value: "2", readonly: true}
  AuthorizeRemoteTxRequests: {value: "false"}

answers:
  UnlockConnector: {status: Unlocked}
  # UpdateFirmware: reject
  # DataTransfer: {status: UnknownVendorId}
  # ReserveNow: not-supported
```

This is the built-in model. Decoding is strict: a field ocppctl does not know is an error, so that a typo does not go unnoticed. JSON is read the same way (`ocppctl client generate-config --format json` writes this model as JSON). Durations are Go durations: `30s`, `5m`, `1h30m`.

### `station`

| Field | Type | Meaning |
|---|---|---|
| `vendor` | string, required | `chargePointVendor` (1.6) / `chargingStation.vendorName` (2.x); at most 20 characters when 1.6 is offered, 50 otherwise |
| `model` | string, required | `chargePointModel` / `chargingStation.model`; at most 20 characters |
| `serial` | string | `chargePointSerialNumber` / `chargingStation.serialNumber` |
| `firmware` | string | `firmwareVersion` |

### `evses`

| Field | Type | Meaning |
|---|---|---|
| `evses[].id` | integer ≥ 1, unique | the EVSE id (2.x `evseId`, `evse.id`) |
| `evses[].connectors` | list of integers ≥ 1, unique per EVSE | the connector ids of the EVSE (2.x `connectorId`) |

At least one EVSE, each with at least one connector.

**OCPP 1.6 connector numbering.** OCPP 1.6 has connectors but no EVSEs. The station numbers its connectors across the EVSEs in the order the model lists them: with `evses: [{id: 1, connectors: [1, 2]}, {id: 2, connectors: [1]}]` the 1.6 connectors are 1 (EVSE 1 connector 1), 2 (EVSE 1 connector 2) and 3 (EVSE 2 connector 1). Sessions in the model are always given as EVSE and connector; a 1.6 `connectorId` from the CSMS (RemoteStartTransaction) is mapped back the same way. Connector 0 (the whole station) is not reported.

### Intervals and power

| Field | Type | Default | Meaning |
|---|---|---|---|
| `heartbeat_interval` | duration ≥ 1s | the CSMS's | the Heartbeat interval. Leave it out to use the `interval` of the CSMS's BootNotification answer (300 s when it gives none) |
| `meter_interval` | duration ≥ 1s | `60s` | how often a running session reports its meter, unless the session sets its own |
| `remote_power_w` | number | `11000` | the charging power of a session the CSMS starts remotely, in W |

### `sessions`

Charging sessions the station starts by itself, counted from its first accepted BootNotification.

| Field | Type | Meaning |
|---|---|---|
| `after` | duration ≥ 0, required | when to start, after the first accepted boot |
| `evse`, `connector` | integers, required | where: a connector the model has |
| `id_token` | string, required | the idTag (1.6) or idToken (2.x) that starts it |
| `token_type` | string | the 2.x idToken type (default `ISO14443`) |
| `power_w` | number ≥ 0 | the charging power, in W: the energy register grows by `power_w` × time |
| `meter_interval` | duration ≥ 1s | how often it reports its meter (default: the model's `meter_interval`, else 60 s) |
| `duration` | duration > 0, required | when to stop |

A session whose connector is busy when it is due is not started (an `error` event says so).

### `device_model` (OCPP 2.x)

The variables GetVariables, SetVariables and GetBaseReport work on, named `Component.Variable`:

| Field | Type | Meaning |
|---|---|---|
| `value` | string | the Actual value |
| `mutability` | `ReadOnly`, `ReadWrite`, `WriteOnly` | how SetVariables and GetVariables treat it (empty: like ReadWrite) |

Only the component and variable names are used: component instances and EVSEs in a request are not matched.

### `configuration` (OCPP 1.6)

The keys GetConfiguration and ChangeConfiguration work on:

| Field | Type | Meaning |
|---|---|---|
| `value` | string | the value |
| `readonly` | bool | ChangeConfiguration is Rejected |

The values in `device_model` and `configuration` are what the CSMS reads and writes; they are not the station's settings, with one exception: a ChangeConfiguration of `HeartbeatInterval` or a SetVariables of `OCPPCommCtrlr.HeartbeatInterval` (a positive number of seconds) also changes the interval the station heartbeats at. The initial value of these entries does not set the interval: `heartbeat_interval` or the CSMS's BootNotification answer does. Without `heartbeat_interval`, the interval of the accepted BootNotification answer is also written into these two entries, so that the CSMS reads back the interval in use.

### `answers`

How to answer an action the CSMS sends, by action name:

| Value | Answer |
|---|---|
| `accept` | `{"status":"Accepted"}`, or `{}` when that is what the action's response schema allows |
| `reject` | `{"status":"Rejected"}`; the CALLERROR `NotSupported` when the action's response has no such status |
| `not-supported` | the CALLERROR `NotSupported` |
| `not-implemented` | the CALLERROR `NotImplemented` |
| an object | that response payload, as it is |

A model may serve several versions, so an object is used only for a version whose response schema it passes; for any other version the station answers as if the entry were not there.

### Validation

Before connecting, the model is checked and every problem reported:

- `station.vendor` and `station.model` are required; `model` is at most 20 characters, `vendor` at most 20 when OCPP 1.6 is offered and 50 otherwise;
- at least one EVSE; EVSE ids ≥ 1 and unique; at least one connector per EVSE, ids ≥ 1 and unique within the EVSE;
- `heartbeat_interval` and every `meter_interval` at least 1s (or left out);
- every session names a connector the model has, has an `id_token`, a non-negative `power_w` and `after`, and a positive `duration`;
- `device_model` names are `Component.Variable`, and mutabilities one of the three;
- every `answers` entry names an action a CSMS sends in some OCPP version (not one the station sends), its value is one of the four behaviours or an object, and an object is a valid response in at least one of the versions offered (`--ocpp-version`) that have the action.

```console
$ ocppctl client run --url ws://localhost:9000/X --insecure-transport --config bad.yaml
error: bad.yaml: evses[0]: connector 1: connector ids start at 1 and are unique per EVSE
sessions[0]: EVSE 2 has no connector 1
sessions[0]: id_token is required
answers: BootNotification is sent by the station, not answered by it
answers: Reset: the response is not valid for any of OCPP 2.1, 2.0.1, 1.6
```

## What the station does

1. **Connects** to `--url`, offering the versions of `--ocpp-version` (default 2.1, 2.0.1, 1.6); the CSMS picks one (`connected` event).
2. **Boots.** It sends a BootNotification (2.x reason `PowerUp`) until the CSMS answers Accepted. While the answer is Pending or Rejected it waits the `interval` of the answer (10 s when there is none, or when the BootNotification failed) and sends it again (`boot_pending`, `boot_rejected` events). A TriggerMessage for BootNotification in the meantime sends one at once. Once Accepted: `booted`.
3. **Reports its connectors**: a StatusNotification for every connector, `Available` (or `Charging` in 1.6 / `Occupied` in 2.x for a connector with a running session).
4. **Heartbeats** at the interval: `heartbeat_interval` of the model; else the `interval` of the accepted BootNotification answer; else 300 s. A ChangeConfiguration or SetVariables of the heartbeat interval changes it while running.
5. **Charges the model's sessions**, scheduled once, from the first accepted boot:

| Step | OCPP 1.6 | OCPP 2.x |
|---|---|---|
| start | StartTransaction (`connectorId`, `idTag`, `meterStart` 0), then StatusNotification `Charging`. The transaction id is the one the CSMS answers | StatusNotification `Occupied`, then TransactionEvent `Started` (`seqNo` 0, `triggerReason` `Authorized`, the idToken, a new transaction id the station makes up, the energy register at 0 Wh) |
| every `meter_interval` | MeterValues with `connectorId`, `transactionId`, `Energy.Active.Import.Register` (Wh) and `Power.Active.Import` (W) | TransactionEvent `Updated` (`triggerReason` `MeterValuePeriodic`, next `seqNo`) with the same two measurands |
| after `duration` | StopTransaction with `meterStop` (reason Local, which 1.6 leaves out), then StatusNotification `Available` | TransactionEvent `Ended` (`stoppedReason` `Local`, `triggerReason` `StopAuthorized`), then StatusNotification `Available` |

The energy register of a session starts at 0 Wh and grows by its power over time, reported in whole Wh. When the CSMS's answer to the start carries an `idTagInfo` / `idTokenInfo` status other than Accepted, the session is stopped at once (1.6 reason `DeAuthorized`; 2.x `stoppedReason` `DeAuthorized`, `triggerReason` `Deauthorized`). The station does not send an Authorize before starting a session.

Every request the station builds is validated against the schema before it is sent, and every response it receives after it arrives; a failure is a `call_error` event. Nothing is sent a second time.

## How the station answers the CSMS

Every CALL from the CSMS is answered in this order:

1. The request is validated against the OCA schema of the session's version. An invalid one is answered with the CALLERROR of the violation (`FormationViolation` / `FormatViolation`, `OccurenceConstraintViolation` / `OccurrenceConstraintViolation`, …).
2. **The model's `answers`** entry for the action, if there is one (see [answers](#answers)). It replaces the built-in behaviour: `Reset: accept` answers Accepted and does not reset.
3. **The built-in behaviour** of the action, for the actions below.
4. **A default**: `{}` when the action's response schema accepts it, else `{"status":"Accepted"}` when it accepts that.
5. Otherwise the CALLERROR `NotSupported`.

Every answer is validated against the response schema before it is sent. One that fails (a model answer cannot, it is checked when the model loads) is replaced by the CALLERROR `InternalError` and reported as an `error` event.

The defaults of step 4 accept what they answer without acting on it: a ChangeAvailability is Accepted and the connector stays as it is, a SetChargingProfile is Accepted and not stored, a ClearCache is Accepted. An action whose response has neither form, such as UnlockConnector (statuses `Unlocked`, `UnlockFailed`, …), gets `NotSupported` unless the model answers it, which is why the built-in model has `UnlockConnector: {status: Unlocked}`. SENDs from the CSMS (OCPP 2.1) are reported as `send_received` events.

## Built-in behaviours

| Action | Version | Behaviour |
|---|---|---|
| RemoteStartTransaction | 1.6 | `connectorId` (numbered across the EVSEs), or the first free connector without one. Accepted, then a session starts as above, with the `idTag` of the request and `remote_power_w`; it runs until stopped by the CSMS, a Reset or the end of the run. Rejected when the connector is busy or does not exist, or no connector is free |
| RequestStartTransaction | 2.x | `evseId`, or the first EVSE with a free connector without one. Answered Accepted with the `transactionId` of the new session, which then starts as above (TransactionEvent `Started` with `triggerReason` `RemoteStart` and the request's `remoteStartId`). Rejected when nothing is free. A `chargingProfile` in the request is ignored |
| RemoteStopTransaction | 1.6 | Accepted when `transactionId` is a running session, which is stopped: StopTransaction with reason `Remote`, StatusNotification `Available`. Rejected otherwise |
| RequestStopTransaction | 2.x | Accepted when `transactionId` is a running session, which is stopped: TransactionEvent `Ended` with `stoppedReason` `Remote` and `triggerReason` `RemoteStop`, StatusNotification `Available`. Rejected otherwise |
| Reset | 1.6, 2.x | 1.6 `Hard` and `Soft`: Accepted and carried out at once, sessions stopped with reason `HardReset` or `SoftReset`. 2.x `Immediate` (and 2.1 `ImmediateAndResume`): Accepted and carried out at once, sessions stopped with `stoppedReason` `ImmediateReset`, `triggerReason` `ResetCommand`. 2.x `OnIdle`: Accepted at once when nothing charges; while a session runs it is answered `Scheduled` and carried out when the last session ends. A 2.x Reset of one EVSE (`evseId`) is Rejected. Carrying out a Reset: a `reset` event, the sessions stopped, the connection closed, and the station connects again and boots with reason `RemoteReset` |
| TriggerMessage | 1.6, 2.x | Accepted for `BootNotification` (2.x reason `Triggered`), `Heartbeat`, `StatusNotification` (every connector; an `evse` / `connectorId` in the request is not used), `MeterValues` (the first EVSE, 1.6 connector 1, energy 0 Wh), `FirmwareStatusNotification`, `DiagnosticsStatusNotification` (1.6) and `LogStatusNotification` (2.x), the last three with status `Idle`; the message is sent after the answer. `NotImplemented` for any other requested message |
| GetConfiguration | 1.6 | `configurationKey` with the keys asked for (all keys when none is asked for), `unknownKey` with those the model does not have |
| ChangeConfiguration | 1.6 | `NotSupported` for a key the model does not have, `Rejected` for a `readonly` one, else `Accepted` and stored. `HeartbeatInterval` also changes the heartbeat interval |
| GetVariables | 2.x | per entry: `NotSupportedAttributeType` for an attribute type other than `Actual`; `UnknownComponent` when the model has no variable of the component; `UnknownVariable`; `Rejected` for a `WriteOnly` variable; else `Accepted` with the `attributeValue` |
| SetVariables | 2.x | per entry: as GetVariables, with `Rejected` for a `ReadOnly` variable; else `Accepted` and stored. `OCPPCommCtrlr.HeartbeatInterval` also changes the heartbeat interval. Values are not checked against a data type |
| GetBaseReport | 2.x | Accepted (for every report base), then one NotifyReport (`seqNo` 0, `tbc` false) with every variable of `device_model`: its Actual value and mutability, data type `string` |
| SetVariableMonitoring | 2.x | per entry: `UnknownComponent`, `UnknownVariable`, else `Rejected`: the simulator sets no monitors |
| ClearVariableMonitoring | 2.x | `NotFound` for every id |
| GetLocalListVersion | 1.6, 2.x | no local authorization list: 1.6 `listVersion` -1, 2.x `versionNumber` 0 |
| GetTransactionStatus | 2.x | `messagesInQueue` false; with a `transactionId`, `ongoingIndicator` says whether that session is running |
| CustomerInformation | 2.x | Rejected: the simulator holds no customer data, and sends no NotifyCustomerInformation |
| ClearTariffs | 2.1 | `NoTariff`: the simulator holds no tariffs |
| UnpublishFirmware | 2.x | `NoFirmware` |

The device model, the configuration keys and the transactions live in memory: a new run starts from the model again.

## Connections and reconnection

- **Lost connection.** The station reconnects with go-ocpp's back-off, the algorithm of OCPP 2.x Part 4 §5.4 with go-ocpp's defaults: attempt *n* waits 5 s × 2^min(n−1, 5) plus up to 10 s at random. The CALLs that were in flight fail (`call_error`) and are not sent again. Once connected again, the station stays booted (a reconnection is not a reboot: OCPP 2.x boots after a power up or a reset) and sends the StatusNotifications of its connectors again; its sessions go on. The events are `disconnected`, `reconnecting`, then `connected` with message `reconnected`. With `--no-reconnect` the station ends instead (exit 4, `connection_lost`).
- **Reset.** The station writes its answer, closes the connection gracefully, is gone for 2 s, as a charger that restarts, and connects again, retrying after 1 s, 2 s, 4 s, … up to one minute between attempts (`reconnecting` events), then boots with reason `RemoteReset` (`connected`, `booted`).
- **Keepalive.** `--ping-interval` sends WebSocket pings; without it the station sends none.

## Many stations

A `{N..M}` range in the identity of `--url` runs one station per number, for load tests:

```console
ocppctl client run --url 'wss://csms.example.com/ocpp/LOAD{001..100}' --stagger 50ms --duration 15m --format json > events.jsonl
```

- One range per URL, ascending, at most 10000 stations. The width of N pads the numbers: `{001..100}` runs LOAD001 to LOAD100, `{1..100}` runs LOAD1 to LOAD100. Quote the URL in the shell.
- Every station uses the same model and flags, and the same password (`--password-file`, `OCPPCTL_PASSWORD`): use a CSMS that accepts one password for all of them, or none.
- `--stagger` delays the start of each station after the first.
- Each station runs on its own; events carry their `station`.
- At the end a summary goes to stderr:

  ```
  summary: 3 stations, 3 booted, 0 failed; 9 calls, 0 errors, latency avg 0s max 1ms; 0 sessions
  ```

  with `failed: <identities>` on a second line when stations failed. The latency is that of the CALLs the stations sent.
- The exit code is 0 when every station ran, 7 when some failed (a refused first connection, or a lost connection with `--no-reconnect`) and others ran, and the error of the first when all failed.

## Events

`client run` writes one event per line to stdout: text, a JSON object (`--format json`) or a table row (`--format table`). The JSON fields are in [json-output.md](json-output.md#client-run-station-events).

| Event | When |
|---|---|
| `connected` | a connection was made (`message`: `tls=… compression=…`) |
| `booted` | the BootNotification was Accepted (`message`: the heartbeat interval) |
| `boot_pending`, `boot_rejected` | it was answered Pending or Rejected (`message`: when it is retried) |
| `call_sent`, `call_result`, `call_error` | a CALL of the station, its result, or its failure |
| `call_received`, `answered` | a CALL of the CSMS, and the answer |
| `send_received` | a SEND of the CSMS (2.1) |
| `session_started`, `session_ended` | a session, with its `transaction` id |
| `reset` | a Reset was accepted |
| `disconnected`, `reconnecting` | the connection was lost, and is being made again |
| `error` | something went wrong in the station |

```console
$ ocppctl client run --url ws://localhost:9000/CS201 --insecure-transport --ocpp-version 2.0.1
20:49:35.361 CS201 connected tls=false compression=false
20:49:35.362 CS201 call_sent BootNotification {"chargingStation":{"firmwareVersion":"1.0.0","model":"Simulator","serialNumber":"SIM-0001","vendorName":"OTFabric"},"reason":"PowerUp"}
20:49:35.362 CS201 call_result BootNotification 0 ms {"currentTime":"2026-10-18T20:49:35.362Z","interval":300,"status":"Accepted"}
20:49:35.362 CS201 booted heartbeat every 5m0s
20:49:35.362 CS201 call_sent StatusNotification {"connectorId":1,"connectorStatus":"Available","evseId":1,"timestamp":"2026-10-18T20:49:35.362Z"}
20:49:35.362 CS201 call_result StatusNotification 0 ms {}
20:49:44.660 CS201 call_received RequestStartTransaction {"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}
20:49:44.661 CS201 answered RequestStartTransaction {"status":"Accepted","transactionId":"0445ef4c-9e0a-80d0-5595-c06a9774b839"}
20:49:44.661 CS201 call_sent StatusNotification {"connectorId":1,"connectorStatus":"Occupied","evseId":1,"timestamp":"2026-10-18T20:49:44.661Z"}
20:49:44.661 CS201 call_result StatusNotification 0 ms {}
20:49:44.661 CS201 call_sent TransactionEvent {"eventType":"Started",…,"triggerReason":"RemoteStart"}
20:49:44.662 CS201 call_result TransactionEvent 0 ms {"idTokenInfo":{"status":"Accepted"}}
20:49:44.662 CS201 session_started transaction 0445ef4c-9e0a-80d0-5595-c06a9774b839 EVSE 1 connector 1, token TESTTOKEN, Accepted
```

Only the transactions:

```console
ocppctl client run --url ws://localhost:9000/CS001 --insecure-transport --format json |
  jq -c 'select(.event | startswith("session_"))'
```
