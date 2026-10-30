# ocppctl Releases

## v0.2.0

**Date:** unreleased
**Previous release:** v0.1.1

### Summary

**Breaking for scripts:** the exit codes follow the OT Fabric organisation table (ADR-0006 of otfabric/engineering). Nothing else changes in the commands.

### Exit codes

| Case | Old | New | JSON error |
|---|---|---|---|
| `context_canceled` (interrupted before a result) | 10 | 130 | kind `interrupted`, was `internal` |

Every other exit code is unchanged. See [docs/exitcodes.md](docs/exitcodes.md).

### Environment

`OCPPCTL_FORMAT` is now the canonical name of the format variable; `OCPPCTL_OUTPUT_FORMAT` stays accepted, and `OCPPCTL_FORMAT` wins when both are set.

---

## v0.1.1

**Date:** 2026-10-19  
**Previous release:** v0.1.0

### Summary

ocppctl moves to [go-ocpp](https://github.com/otfabric/go-ocpp) v0.1.3, which fixes the three go-ocpp bugs found while building ocppctl, and is qualified against the five reference implementations of [ocpp-interop](https://github.com/otfabric/ocpp-interop) v0.4.1, CitrineOS among them. Every known issue, limitation and open question, with its root cause, is in [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md).

### Fixed

- **Frames lost on close (G1, go-ocpp v0.1.2).** A SEND queued just before a graceful close, or the answer to a CALL from the peer (a Reset) right before the connection closed, could be lost. ocppctl's own workaround (counting written frames through the frame observer) is removed: every connection now closes with go-ocpp's graceful `Shutdown`, which drains first.
- **`20000.0` refused for an integer field (G2, go-ocpp v0.1.2).** EVerest libocpp's 2.0.1 NotifyEVChargingNeeds was refused by `server start`; it is now accepted, as the draft-06 OCA schemas allow. The draft-04 OCPP 1.6 core schemas still refuse it, as draft-04 requires.
- **A SEND dropped when the peer closes right after it (G3, go-ocpp v0.1.3).** SENDs received before the end of a connection are always delivered.

### Changed

- **`client run`:** after a Reset the simulated station is gone for 2 s before it connects again, as a charger that restarts. An immediate reconnection could be mistaken by a CSMS for the connection it had not finished closing (seen with CitrineOS).
- **`client script`:** when a scenario ends, a CALL from the CSMS held for a step that never ran gets the default answer and is listed as unexpected, so the connection closes gracefully at once.
- **`scripts/interop.sh`:** ocpp-interop v0.4.1 by default, with CitrineOS as a fifth reference (a CSMS for 1.6, 2.0.1 and 2.1). It waits until a reference CSMS has handled a close before it connects again, and until the simulated station has booted again after a Reset. A message the reference never sent (no API for it, its library cannot encode it, it refused the payload), as ocppctl's event log shows, is a skip with the reference's reason, not a failure.

### Quality

- **Interoperability:** against ocpp-interop v0.4.1 (results in `reports/interop-v0.4.1.tsv`, run with go-ocpp v0.1.2; v0.1.3 changes only the delivery of a SEND received right before a close), every fixture sample in both directions: ocppctl as the station against the CSMSs of mobilityhouse (1.6, 2.0.1, 2.1), ChargeTime (1.6, 2.0.1), lorenzodonini (1.6, 2.0.1) and CitrineOS (1.6, 2.0.1, 2.1), and the stations of mobilityhouse, ChargeTime, lorenzodonini and EVerest libocpp (1.6, 2.0.1, 2.1) against ocppctl's server. Of 1218 exchanges, **1154 passed**, 29 were answered with a CALLERROR (9 of them ocppctl's own NotSupported for the PKI and VAT services it does not have), 32 were skipped because the reference did not send the message, and **3 failed**: ocpp-go's schema-invalid 2.0.1 responses, which ocppctl refuses. In v0.1.0 (ocpp-interop v0.3.1, four references): 999 passed, 11 CALLERROR, 8 failed.
- **Tests:** whole-module statement coverage 89.3% (floor 85%). New tests for the `--debug` frame trace, a scenario ending with a held CALL, and a station stopped while it restarts; `make stress` passes.

---

## v0.1.0

**Date:** 2026-10-18  
**Previous release:** none

### Summary

First release. ocppctl is a command-line tool for **OCPP 1.6J, 2.0.1 and 2.1** in both roles: a **Charging Station** that probes a CSMS, sends any message, runs simulated stations from a model and runs scenarios with expectations, and a **CSMS** that accepts stations, answers them from a model, streams what they send and sends them CALLs. Offline commands validate payloads, decode and encode frames and compute certificate hashes. Built on [go-ocpp](https://github.com/otfabric/go-ocpp) v0.1.1; every message is validated against the OCA schemas in both directions.

### Highlights

- **Connecting:** **`--url ws[s]://host[:port]/path/<identity>`**, **`--ocpp-version`** (the versions to offer, default `2.1,2.0.1,1.6`; the CSMS picks one), security profiles 0 to 3 with **`--insecure-transport`**, **`--password-file`** or **`OCPPCTL_PASSWORD`** (never a flag value or the URL), **`--tls-ca`**, **`--tls-cert`**, **`--tls-key`**, **`--tls-server-name`**; **`--compression`**, **`--header`**, **`--timeout`**, **`--connect-timeout`**. TLS 1.2 minimum, and verification cannot be turned off.
- **`client probe`:** Connects and reports the negotiated version, TLS version, cipher suite and CSMS certificate, the authentication, compression and handshake time, without sending a message. **`--each-version`** tries each version on its own. A refused handshake reports its HTTP status and meaning (401, 403, 404, 409, 400, 503).
- **`client call <Action>`:** One CALL with **`--payload`** (JSON, `@file` or `-`), validated against the schema of the negotiated version before it is sent (before connecting with one `--ocpp-version`); the response is validated too. **`--raw`** sends an unvalidated payload. **`--boot`** sends a BootNotification first and requires Accepted (**`--boot-vendor`**, **`--boot-model`**). A state-changing CALL without a response exits 5 with `outcome_unknown` and is never resent. CALLs the CSMS sends meanwhile are answered with a schema-valid default and listed.
- **`client send <Action>`:** One OCPP 2.1 SEND.
- **Shortcuts:** **`client boot`**, **`heartbeat`**, **`authorize`**, **`status`**, **`meter`**, **`data-transfer`**, **`transaction start`** and **`transaction stop`** build the payload for the version the CSMS picks (StartTransaction/StopTransaction in 1.6, TransactionEvent Started/Ended in 2.x). **`--payload-only`** prints the payload for one version, to pipe into `client call`.
- **`client run`:** A simulated station from a YAML or JSON model (**`--config`**, or the built-in one): BootNotification until Accepted, honouring Pending and Rejected; StatusNotifications; heartbeats at the CSMS's interval; charging sessions with meter values; built-in behaviours for remote start and stop, Reset, TriggerMessage, GetConfiguration/ChangeConfiguration, GetVariables/SetVariables/GetBaseReport, SetVariableMonitoring/ClearVariableMonitoring, GetLocalListVersion, GetTransactionStatus, CustomerInformation, ClearTariffs and UnpublishFirmware; per-action answers from the model. Reconnects with the OCPP back-off unless **`--no-reconnect`**; **`--ping-interval`**, **`--duration`**. A **`{N..M}`** identity range runs many stations, **`--stagger`** apart, with a summary of calls, latency and errors; exit 7 when some failed.
- **`client script <file>`:** Scenarios of `call`, `send`, `wait_for`, `expect_no_call`, `sleep`, `disconnect` and `reconnect` steps; expectations by path with equality, `regex`, `present` and `one_of`; `expect_error`; `save` and variables (`${now}`, `${uuid}`, `${identity}`, **`--var`**); payload files; CALLs from the CSMS that arrive early held for their step, a CALL out of order with a `wait_for` failing it at once instead of at its timeout, others answered and listed as unexpected. **`--keep-going`**; exit 0 when every step passed, 7 when some failed.
- **`client generate-config`:** Writes the commented station model, as YAML or JSON.
- **`server start`:** A CSMS for all three versions (**`--ocpp-version`**), on **`--listen`** and **`--path`**: security profiles 0 to 3 with **`--no-auth`**, passwords from the model or **`--passwords-file`**, **`--tls-cert`**/**`--tls-key`** and **`--tls-client-ca`** (identity = certificate CN); a model (**`--config`**) for the boot status and interval per station, the token list, the 1.6 transaction ids and per-action, per-version, per-station answers; **`--allow`**, **`--max-stations`**, **`--ping-interval`**, **`--rate-limit`**, **`--rate-burst`**, **`--duration`**. Every connection, refusal, message and answer is an event on stdout.
- **`server call <Action>`** and **`server stations`:** Send a CALL to a connected station, or list the stations, through the server's control socket (**`--control-socket`**, `ocppctl.sock` in the working directory by default, mode 0600). A CALL that may change the station needs **`--yes`**.
- **`server generate-config`:** Writes the commented CSMS model, as YAML or JSON.
- **`actions`**, **`validate`**, **`frame decode|encode`**, **`cert hash`:** The actions of a version with their sender and kind; schema validation of a request or response (exit 3 when invalid, with the field, the reason and the CALLERROR code); the OCPP-J frame codec; the CertificateHashData of a certificate, with **`--issuer`** and **`--hash sha256|sha384|sha512`**.
- **Output:** **`--format text|json|table`** on every command; streams write JSON Lines. A single JSON error object on failure with `--format json`, with an `ocpp` object for CALLERRORs, schema violations and refused handshakes, and stable exit codes (0, 2, 3, 4, 5, 6, 7, 10). The JSON shapes and exit codes are documented in **`docs/json-output.md`** and **`docs/exitcodes.md`**; the station model, scenarios and the CSMS in **`docs/station-model.md`**, **`docs/scenarios.md`** and **`docs/server-config.md`**.
- **Environment:** Most flags have an **`OCPPCTL_*`** variable; a flag wins over the environment. **`OCPPCTL_PASSWORD`** holds the password itself and is read when the command runs.
- **`--debug`:** Traces every frame sent and received and go-ocpp's log, and shows the causes of an error. Handshake headers and credentials are not traced.
- **Completion:** bash, zsh, fish and PowerShell; flags with a fixed set of values complete to those values.

### Limitations

Every limitation, known issue and open question of this release, with its status, is in **[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)**. In short:


- **No `example` command.** go-ocpp v0.1.1 exports no generator of example payloads; `--payload-only` of the shortcuts and the payloads in `examples/` are the starting points.
- **`server` sends no SENDs** (OCPP 2.1) to stations: `server call` sends CALLs only.
- **The 1.6 transaction ids of `server` restart from `transactions.first_id`** every time the server starts, and are shared by all stations. Nothing the server learns is persisted.
- **The CSMS answers Get15118EVCertificate, GetCertificateChainStatus and VatNumberValidation with the CALLERROR `NotSupported`**: it has no PKI and no VAT service. A model can answer them with a fixed response.
- **The simulated station's built-in behaviours are the listed set** (see `docs/station-model.md`). Everything else is answered with the first schema-valid answer (`{}` or status Accepted) and has no effect: a SetChargingProfile is not stored and GetCompositeSchedule is not computed from it, a ChangeAvailability does not change a connector, an UpdateFirmware or GetLog starts no transfer and no status notifications. A 1.6 Reset and a 2.x Immediate Reset are carried out at once; a 2.x OnIdle Reset waits for the last session. The station does not Authorize before a session, and the device model and configuration values do not change its behaviour, except the heartbeat interval.
- **No OCPP 1.6 Security Whitepaper extensions are simulated** beyond what the generic call and answers cover: ExtendedTriggerMessage, SignCertificate/CertificateSigned, InstallCertificate, GetInstalledCertificateIds, DeleteCertificate, SignedUpdateFirmware and GetLog can be sent with `client call` and `server call`, and are answered by defaults or the model, not by a certificate store or a log.
- **One connection per one-shot command.** `client call` and the shortcuts connect, send and disconnect; a CSMS that requires an accepted boot on the same connection needs `--boot`.
- **Simulated stations of a range share one model and one password.**

### Known issues

- **go-ocpp v0.1.1 refuses a JSON number with a fraction part such as `20000.0` for an integer field** (TypeConstraintViolation), where the OCA schemas accept it. A NotifyEVChargingNeeds from EVerest libocpp 2.0.1 is refused for this reason. To be fixed in go-ocpp.

### Quality

- **Tests:** Whole-module statement coverage above the 85% floor enforced by **`make coverage`** (`COVERAGE_MIN`). Every command is tested through the real command tree, in process, against go-ocpp's `ocpptest` CSMS and stations (`internal/testcsms`) and against ocppctl's own server, in every version, over ws:// and TLS, in all three output formats, with its exit codes.
- **Interoperability:** Run against [ocpp-interop](https://github.com/otfabric/ocpp-interop) v0.3.1 with **`scripts/interop.sh`** (results in `reports/interop-v0.3.1.tsv`), with every fixture sample in both directions: ocppctl as the station against the CSMSs of mobilityhouse (1.6, 2.0.1, 2.1), ChargeTime (1.6, 2.0.1) and lorenzodonini (1.6, 2.0.1), and the stations of mobilityhouse, ChargeTime, lorenzodonini and EVerest libocpp (1.6, 2.0.1, 2.1) against ocppctl's server. 999 exchanges passed, 11 were answered with a CALLERROR and 8 failed, all explained:
  - ocppctl's CSMS answers Get15118EVCertificate, GetCertificateChainStatus and VatNumberValidation with `NotSupported` (see Limitations);
  - NotifyEVChargingNeeds from EVerest 2.0.1 is refused with TypeConstraintViolation (see Known issues);
  - upstream: the lorenzodonini 2.0.1 station sends schema-invalid responses to Reset and SetNetworkProfile (`statusInfo: null`) and GetCompositeSchedule (an extra `evseId`), which ocppctl reports as `invalid_response`; the mobilityhouse 2.1 station rejects the BatterySwap fixture; the mobilityhouse and EVerest adapters cannot send the 2.1 SEND NotifyPeriodicEventStream; EVerest 2.1 cannot send GetCertificateChainStatus and ReportDERControl, and answers NotifyWebPaymentStarted with `NotImplemented`.
- **Hardware-free:** The test suite needs no charger and no network beyond the loopback interface.

---
