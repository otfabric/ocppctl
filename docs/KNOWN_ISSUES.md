# Known issues, limitations and open questions

Everything known to be wrong, missing or undecided in ocppctl v0.1.0, in one place. Each
entry says where the problem lies, what it does to you, and what is done about it. When an
entry is resolved, it moves to the release notes of the release that resolves it.

- [Bugs in go-ocpp](#bugs-in-go-ocpp)
- [Findings in the reference implementations](#findings-in-the-reference-implementations)
- [Limitations of ocppctl](#limitations-of-ocppctl)
- [Open questions](#open-questions)

## Bugs in go-ocpp

ocppctl is built on [go-ocpp](https://github.com/otfabric/go-ocpp) v0.1.1. These are
defects of that library, found while building and testing ocppctl. The fixes belong in
go-ocpp; ocppctl moves to the release that has them.

| # | Bug | Effect on ocppctl | Status |
|---|-----|-------------------|--------|
| G1 | **`Client.Shutdown` and `Close` can drop a frame that is queued but not yet written.** `Endpoint.Idle()` reports idle once the writer has taken a frame off its queue, before the write completes, and does not count queued responses at all; `Close` then cancels the write. A SEND queued just before `Shutdown`, or the CALLRESULT of a handler (for example to a Reset) when the owner closes right after, can be lost. | None visible: ocppctl works around it. `client send`, the simulated station (Reset) and `client script` (answers to `wait_for`, `send` steps) count the frames go-ocpp's frame observer reports as written, and wait for them (at most 2 s) before closing. | Workaround in v0.1.0. Fix to go-ocpp proposed; remove the workaround once it is released. |
| G2 | **Integral floats are refused for integer fields.** go-ocpp decodes payloads into Go types, and a number such as `20000.0` does not decode into an `int`: the CALL is answered with TypeConstraintViolation. The OCA schemas are JSON Schema draft-06, in which `20000.0` *is* an integer. | A peer that writes integers as floats is refused. In the interop run, EVerest libocpp 2.0.1 sends NotifyEVChargingNeeds with `"energyAmount": 20000.0` and ocppctl's CSMS refuses it. | No workaround: the validation is go-ocpp's. Fix to go-ocpp proposed. |

## Findings in the reference implementations

From the interoperability run against [ocpp-interop](https://github.com/otfabric/ocpp-interop)
v0.3.1 (`scripts/interop.sh`, results in [`reports/interop-v0.3.1.tsv`](../reports/interop-v0.3.1.tsv)):
999 exchanges passed, 11 were answered with a CALLERROR and 8 failed. Apart from G2 above
and L3 below, every non-pass lies with the other implementation or its test adapter. ocppctl
reports them; it does not hide or work around them.

| # | Implementation | Finding | What ocppctl does |
|---|----------------|---------|-------------------|
| U1 | lorenzodonini/ocpp-go 2.0.1 station | Responds to Reset and SetNetworkProfile with `"statusInfo": null`, and to GetCompositeSchedule with an `evseId` the schema does not have. | `server call` exits 6, `invalid_response`, with the field in the error. |
| U2 | mobilityhouse/ocpp 2.1 station | Rejects the BatterySwap fixture payload (additional properties). | None needed: the request never reaches ocppctl. |
| U3 | mobilityhouse and EVerest test adapters, 2.1 | Cannot send the SEND NotifyPeriodicEventStream (the adapters know message types 2 to 4 only). | `client send` and `server start` support SEND; untested against these two stacks. |
| U4 | EVerest libocpp 2.1 station | Cannot send GetCertificateChainStatus and ReportDERControl; answers NotifyWebPaymentStarted with NotImplemented. | Reported as received. |

## Limitations of ocppctl

Deliberate gaps of v0.1.0. They are not bugs, but you may run into them.

| # | Limitation | Why, and what to do instead |
|---|------------|-----------------------------|
| L1 | **No `example` command.** | go-ocpp exports no generator of example payloads. Start from `--payload-only` of a shortcut, from `examples/`, or from the ocpp-interop fixtures (`print-fixture`). |
| L2 | **`server` sends no SENDs** (OCPP 2.1) to stations. | `server call` sends CALLs only. |
| L3 | **ocppctl's CSMS answers Get15118EVCertificate, GetCertificateChainStatus and VatNumberValidation with the CALLERROR NotSupported.** | It has no PKI and no VAT service, and a made-up answer would claim one. A model can answer them with a fixed response. |
| L4 | **The 1.6 transaction ids of `server` restart from `transactions.first_id`** on every start, and are shared by all stations; nothing is persisted. | Use a fresh `first_id` per test run when ids must not repeat. |
| L5 | **The simulated station's built-in behaviours are a fixed set** (see [station-model.md](station-model.md)). Everything else is answered with the first schema-valid answer (`{}` or status Accepted) and has no effect: a SetChargingProfile is not stored, GetCompositeSchedule is not computed, ChangeAvailability does not change a connector, UpdateFirmware and GetLog start no transfer and send no status notifications. | Answer such actions explicitly in the model (`answers`), or test them with `client script` and `wait_for`. |
| L6 | **No OCPP 1.6 Security Whitepaper behaviour is simulated** (certificate store, signed firmware, security log). | The messages can be sent with `client call` and `server call`, and are answered by defaults or the model. |
| L7 | **One connection per one-shot command.** | A CSMS that requires an accepted boot on the same connection needs `--boot`; for longer exchanges use `client script` or `client run`. |
| L8 | **Stations of an identity range share one model and one password.** | Run several `client run` processes for different models. |
| L9 | **The simulated station does not Authorize before a session**, and its device model and configuration values do not change its behaviour, except the heartbeat interval. | Script the Authorize with `client script` when the CSMS under test requires it. |

## Open questions

Decisions taken for v0.1.0 that could reasonably go the other way. Feedback decides
whether they stay.

| # | Question | v0.1.0 behaviour | Alternative |
|---|----------|------------------|-------------|
| Q1 | Should a simulated station boot again after an automatic reconnection? | No. It stays booted, reports its connectors with StatusNotification and goes on; it boots after a Reset only. OCPP 2.x B01 boots after a power up or a reset. | Boot after every reconnection, as some stations do. Could become a model option. |
| Q2 | How should a 2.x OnIdle Reset behave during a session? | Answered `Scheduled`, carried out when the last session ends. | Wait for an explicit stop only, or end the sessions as Immediate does. |
| Q3 | Should `server start` accept any path depth? | No: only `<--path>/<identity>`. A wrong path is refused with HTTP 404 and a `refused` event. | Take the identity from the last segment of any path, as the client does. |
| Q4 | Which default answer should a `client script` `wait_for` without `answer` give? | `{"status":"Accepted"}` first, then `{}`, whichever the schema allows. CALLs no step waits for get `{}` first, then Accepted (as one-shot commands do). | One rule for both. |
| Q5 | Should client and server share environment variables? | They do: `OCPPCTL_CONFIG`, `OCPPCTL_TLS_CERT`, `OCPPCTL_TLS_KEY` and `OCPPCTL_OCPP_VERSION` mean the station model or the CSMS model, the client or the server certificate, offered or accepted versions, depending on the command. A list in `OCPPCTL_OCPP_VERSION` makes `actions`, `validate` and `frame` (one version) exit 3. | Role-specific names (`OCPPCTL_SERVER_*`). |
| Q6 | Where should the control socket of `server start` live? | `ocppctl.sock` in the working directory (mode 0600), so that `server call` and `server stations` find it when run from the same directory; `--control-socket` or `OCPPCTL_CONTROL_SOCKET` elsewhere. | A per-user runtime directory (`$XDG_RUNTIME_DIR`). |
| Q7 | Should the CSMS answer DataTransfer with `UnknownVendorId` by default? | Yes: OCPP requires that of a recipient with no implementation for the vendor. A model can answer otherwise. | Accepted, as many test CSMSs do. |
