# Known issues, limitations and open questions

Everything known to be wrong, missing or undecided in ocppctl v0.1.0, in one place. Each
entry says where the problem lies, what it does to you, and what is done about it. When an
entry is resolved, it moves to the release notes of the release that resolves it.

- [Bugs in go-ocpp](#bugs-in-go-ocpp)
- [Findings in the reference implementations](#findings-in-the-reference-implementations)
- [Limitations of ocppctl](#limitations-of-ocppctl)
- [Open questions](#open-questions)

## Bugs in go-ocpp

ocppctl is built on [go-ocpp](https://github.com/otfabric/go-ocpp). These are defects of
that library, found while building and testing ocppctl. All three are fixed (G1 and G2
in go-ocpp v0.1.2, G3 in v0.1.3); ocppctl v0.1.0 requires go-ocpp v0.1.3 and has no
workaround of its own any more.

| # | Bug | Effect on ocppctl | Status |
|---|-----|-------------------|--------|
| G1 | **`Client.Shutdown` and `Close` could drop a frame that was queued but not yet written.** The endpoint counted itself idle once its writer had taken a frame off the queue, before the write completed, and did not count queued answers; `Close` then cancelled the write. A SEND queued just before `Shutdown`, or the answer of a handler (to a Reset, for one) when the owner closed right after, could be lost. | `client send` could lose its SEND; the simulated station could lose its answer to a Reset; `client script` could lose the answer to a `wait_for` or a `send` step at the end. | **Fixed in go-ocpp v0.1.2**: the engine counts every frame until it is written, and `Shutdown` drains (new CALLs and SENDs refused, what is in flight finished). ocppctl closes every connection with go-ocpp's graceful `Shutdown`; its workaround (counting written frames through the frame observer) is removed. |
| G2 | **Integral numbers were refused for integer fields.** go-ocpp decoded `20000.0` into a Go `int` and failed: TypeConstraintViolation. The OCPP 1.6 Security, 2.0.1 and 2.1 schemas are JSON Schema draft-06, in which `20000.0` is an integer. | A peer that writes integers as floats was refused: EVerest libocpp 2.0.1 sends NotifyEVChargingNeeds with `"energyAmount": 20000.0`, and ocppctl's CSMS refused it. | **Fixed in go-ocpp v0.1.2**: the integer rule follows each schema's draft (draft-06 accepts `20000.0`; the draft-04 1.6 core schemas still refuse it, as draft-04 requires). EVerest's NotifyEVChargingNeeds now passes against ocppctl's CSMS. |
| G3 | **A SEND received just before the peer closed could be dropped.** When the close frame was read before the dispatcher had taken the SEND off its queue, the dispatcher chose at random between the SEND and the end of the connection. Found by the release run of go-ocpp v0.1.2. | A station that sends an OCPP 2.1 SEND and disconnects at once could have the SEND dropped by ocppctl's CSMS (and the other way round for `client run`). | **Fixed in go-ocpp v0.1.3**: SENDs received before the end are always delivered; CALLs that have not started are not dispatched once the connection has ended, since they can no longer be answered. |

## Findings in the reference implementations

From the interoperability run against [ocpp-interop](https://github.com/otfabric/ocpp-interop)
v0.4.1, run with go-ocpp v0.1.2 (`scripts/interop.sh`, results in
[`reports/interop-v0.4.1.tsv`](../reports/interop-v0.4.1.tsv)): every fixture sample in both
directions, five references. **1154 exchanges passed, 29 were answered with a CALLERROR,
32 were skipped and 3 failed.**

- The 3 failures are U1: responses of ocpp-go that do not conform to the OCA schema, which
  ocppctl refuses (`invalid_response`), as it should.
- The 32 skips are messages the reference did not send: ocppctl's event log shows it never
  received them (U2, U3, U4, U5).
- The 29 CALLERRORs are answers: 9 are ocppctl's own NotSupported for the services it does
  not have (L3), 20 are CitrineOS's and EVerest's (U4, U5).

Every non-pass lies with the other implementation or its test adapter, or with L3. ocppctl
reports them; it does not hide or work around them. The root causes were traced in
ocpp-interop v0.4.1 ([otfabric/ocpp-interop#7](https://github.com/otfabric/ocpp-interop/pull/7)),
which fixed what was the adapters' (the EVerest adapter declared too few actions) and found
nothing the adapters can honestly change for these: the pinned upstreams have no newer
release that fixes them. The results are the same as against v0.4.0: the script sends every
fixture sample, whatever an adapter declares.

| # | Implementation | Finding | Root cause | What ocppctl does |
|---|----------------|---------|------------|-------------------|
| U1 | lorenzodonini/ocpp-go v0.19.0, 2.0.1 station | Responds to Reset and SetNetworkProfile with `"statusInfo": null`, and to GetCompositeSchedule with a top-level `evseId` the schema does not have. | ocpp-go's response types: `StatusInfo` without `omitempty` (`ocpp2.0.1/provisioning/reset.go` L61-64, `set_network_profile.go` L170-173); `GetCompositeScheduleResponse` writes `evseId` (`smartcharging/get_composite_schedule.go` L46-50). The `evseId` is removed on ocpp-go's master (unreleased). | `server call` exits 6, `invalid_response`, with the field in the error. |
| U2 | mobilityhouse/ocpp 2.1.0, 2.1 station | Refuses to send the BatterySwap fixture (additional properties `SoC`, `soh`). | The fixture is right (`soC`, `soH`, as the OCA schema). The library's case conversion turns them into `SoC` and `soh` (`ocpp/charge_point.py` L57, L399), which its own validation then refuses (L414). | None needed: the message never reaches ocppctl. BatterySwap passes from the EVerest station. |
| U3 | mobilityhouse 2.1.0 and EVerest libocpp, 2.1 stations | Cannot send the SEND NotifyPeriodicEventStream. | Neither library has OCPP-J message type 6 (`ocpp.messages.MessageType`; libocpp `common/call_types.hpp` L47-52). | Skipped. ocppctl sends and receives SENDs (tested against itself and go-ocpp). |
| U4 | EVerest libocpp (everest-core 2026.02.1), 2.1 station | Cannot send GetCertificateChainStatus and ReportDERControl; answers NotifyWebPaymentStarted with NotImplemented. | Missing from `ocpp::v2::MessageType` (`include/ocpp/v2/types.hpp`). ReportDERControl is on everest-core's main, not in a release. | Skipped (cannot send); the CALLERROR is reported as received. |
| U5 | CitrineOS core v2.0.1 (CSMS, 1.6, 2.0.1, 2.1) | Its message API has no route for 22 action/version pairs (for example ReserveNow, GetLog and InstallCertificate in 1.6; most 2.1 additions such as the tariff, DER and periodic-event-stream messages), and it refuses to send five fixture samples (SignedUpdateFirmware in 1.6; CancelReservation, no such reservation, and InstallCertificate, which it parses, in 2.0.1 and 2.1). It answers the 1.6 Security Whitepaper messages with NotImplemented, NotifyCustomerInformation and NotifyDisplayMessages with PropertyConstraintViolation (a `requestId` it did not send), and SignCertificate (2.0.1, 2.1) and nine other 2.1 messages with FormatViolation although the samples conform to the OCA schemas. It serves TLS only to clients that send SNI, and mixes up two connections of the same identity when the second opens before it has handled the close of the first. | CitrineOS's own behaviour (the adapter answers nothing itself: `fixtureAnswers: false`). The TLS and reconnection findings are recorded with evidence in ocpp-interop and go-ocpp. | Skipped (cannot send) or reported as answered. The interop script waits until CitrineOS has handled a close before connecting again, and the simulated station waits 2 s after a Reset before it reconnects, as a restarting charger does. |

Also found, not involving ocppctl: the mobilityhouse CSMS validates the draft-06 schemas with
a draft-04 validator and so refuses EVerest's `20000.0` (the same case as G2), recorded in
ocpp-interop's known bugs.

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
