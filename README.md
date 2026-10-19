# ocppctl — OCPP 1.6J, 2.0.1 & 2.1 Command-Line Tool: Probe a CSMS, Send Messages, Simulate Charging Stations, Run Scenarios & Serve a Test CSMS

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI Status](https://github.com/otfabric/ocppctl/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/ocppctl/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/ocppctl/graph/badge.svg)](https://codecov.io/gh/otfabric/ocppctl)
[![Latest Release](https://img.shields.io/github/v/release/otfabric/ocppctl?label=release)](https://github.com/otfabric/ocppctl/releases)

**ocppctl is a command-line toolkit for OCPP (Open Charge Point Protocol), the protocol between EV charging stations and their central system, in versions 1.6J, 2.0.1 and 2.1 and in both roles.** As a **Charging Station** it probes a CSMS (which version, which security profile, is the certificate right), sends any message with schema validation in both directions, runs one or thousands of simulated stations that boot, heartbeat, charge and answer the CSMS, and runs scenarios with expectations for CI. As a **CSMS** it accepts real or simulated stations, answers them from a model, streams everything they send, and sends them CALLs on request. It also validates payloads, decodes frames and computes certificate hashes offline.

It is built for CSMS development and QA, station and firmware development, commissioning and troubleshooting, and hardware-free test setups. Output can be plain text, a table or JSON with stable exit codes, so it works in scripts and CI. It is written in Go on top of [go-ocpp](https://github.com/otfabric/go-ocpp) v0.1.1: every message on the wire is go-ocpp's, validated against the JSON schemas of the Open Charge Alliance.

**Quick links:** [Quick start](#quick-start) · [Features](#features) · [Installation](#installation) · [OCPP in two minutes](#ocpp-in-two-minutes) · [Connecting](#connecting-to-a-csms) · [Runbook](#runbook) · [Simulated station](#simulate-a-station) · [Scenarios](#scenarios) · [CSMS](#run-a-csms) · [FAQ](#faq)

> **Responsible use:** OCPP controls real chargers and real money. Only use ocppctl on systems you own or are authorized to test. `server call` resets stations, starts and stops charging sessions and changes their configuration; `client call` and the simulated stations start and stop transactions a CSMS may bill; a simulated station under the identity of a real one can push that station off its CSMS. Use test identities and test tokens, and know what a message does before you send it.

---

## Why ocppctl?

- **Is the CSMS reachable, and what does it accept?** — `client probe` reports the OCPP version the CSMS picks, the TLS version and certificate, the authentication and compression, and with `--each-version` every version it accepts. A refused handshake comes with its HTTP status and what it usually means.
- **Send any message** — `client call <Action>` sends one CALL with a payload from the command line, a file or stdin, validated against the OCA schema before it is sent; the response is validated too. `--raw` sends what you give it, to see how a CSMS copes.
- **Common messages without typing JSON** — `client boot`, `heartbeat`, `authorize`, `status`, `meter`, `data-transfer` and `transaction start|stop` build the payload for whichever version the CSMS picks; `--payload-only` prints it instead.
- **Simulate stations** — `client run` keeps a station connected that boots, heartbeats, charges sessions with meter values, and answers remote start and stop, reset, trigger, configuration and device model requests. `--url …/CS{001..100}` runs a hundred of them.
- **Regression-test a CSMS** — `client script` runs a YAML scenario of calls, expected answers and expected CALLs from the CSMS, and exits 0 or 7.
- **A CSMS on your laptop** — `server start` accepts stations of all three versions and security profiles 0 to 3, answers them from a model, prints every message, and `server call` sends them Reset, RequestStartTransaction or anything else.
- **Offline** — `validate` a payload, `frame decode` a line from a log, list the `actions` of a version, compute the `cert hash` that DeleteCertificate needs.
- **Strict by default** — every message is validated against the OCA schemas, in both directions; TLS verification cannot be turned off; passwords are never taken from the command line.
- **Script-friendly** — `--format text|json|table`, JSON Lines for streams, a single JSON error object on failure, stable exit codes and `OCPPCTL_*` environment variables.
- **Pre-built binaries** — Linux, macOS and Windows (x86-64 and ARM) from [GitHub Releases](https://github.com/otfabric/ocppctl/releases), with SHA-256 checksums; no runtime to install.

---

## Quick start

Download a binary from [GitHub Releases](https://github.com/otfabric/ocppctl/releases) (see [Installation](#installation) for copy-paste commands for Linux, macOS and Windows, or build from source). No CSMS at hand? Start one in a terminal:

```console
ocppctl server start --insecure-transport --no-auth
```

and talk to it from another (replace the URL with your CSMS endpoint and a station identity):

```console
# Which OCPP version does the CSMS pick?
ocppctl client probe --url ws://localhost:9000/CS001 --insecure-transport

# Register the station: Accepted, Pending or Rejected?
ocppctl client boot --url ws://localhost:9000/CS001 --insecure-transport

# Any message, with a JSON payload
ocppctl client call Authorize --url ws://localhost:9000/CS001 --insecure-transport \
  --ocpp-version 2.0.1 --payload '{"idToken":{"idToken":"04A1B2C3D4E5F6","type":"ISO14443"}}'

# A simulated station that stays connected and charges by itself
ocppctl client run --url ws://localhost:9000/CS002 --insecure-transport

# From the CSMS side: which stations are connected, and start a session on one
ocppctl server stations
ocppctl server call RequestStartTransaction --station CS002 --yes \
  --payload '{"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}'
```

A real CSMS uses TLS: `--url wss://csms.example.com/ocpp/CS001`, with a password from `--password-file` or `OCPPCTL_PASSWORD` (security profile 2), or a client certificate (`--tls-cert`, `--tls-key`, profile 3). See [Connecting to a CSMS](#connecting-to-a-csms).

---

## How it compares

OCPP test tools are mostly graphical simulators with a window per station, CSMS products with a test mode, or the OCA's own compliance test tool for certification. Protocol libraries are the other end of the range: they do everything, once you have written a program. ocppctl sits in between.

| | ocppctl | GUI station simulators | OCPP libraries | Wireshark |
|---|:---:|:---:|:---:|:---:|
| Send any message to a CSMS without writing code | yes | yes | — | — |
| Schema validation of every message, both directions | yes | some | some | — |
| Works over SSH, in a container, in CI | yes | — | yes | partial (tshark) |
| Scriptable: JSON output, exit codes, environment variables | yes | rarely | yes (it is code) | partial |
| Hundreds of simulated stations from one command | yes | rarely | with code | — |
| A CSMS to point a real station at | yes | — | with code | — |
| Decodes traffic between two other parties (TLS aside) | — | — | — | yes |
| Certification of a product | — | — | — | — |

ocppctl is a test and diagnostic tool, not a compliance test: passing a scenario says what you checked, not that a product conforms. Use the OCA's certification programme for that.

---

## Use cases

- **CSMS development** — Fire single messages at your CSMS while you build it, with the response validated; replay a frame from a log; send an invalid one with `--raw`.
- **CSMS regression tests in CI** — A scenario per behaviour, `client script --format json`, exit codes in the pipeline.
- **Load and soak tests** — `client run --url …/LOAD{0001..1000} --stagger 20ms --duration 1h` and a summary of calls, latency and errors.
- **Station and firmware development** — Point the station at `server start`, watch every message it sends, and send it Reset, TriggerMessage, GetVariables or a remote start from another terminal.
- **Commissioning and troubleshooting** — Is the CSMS reachable from this site, with this certificate and password? Which version does it pick? Does it accept this station? `client probe` and `client boot` answer in a second.
- **Certificate management** — The CertificateHashData of a certificate for DeleteCertificate and GetCertificateStatus, from its PEM file.

---

## Table of Contents

- [Why ocppctl?](#why-ocppctl)
- [Quick start](#quick-start)
- [How it compares](#how-it-compares)
- [Use cases](#use-cases)
- [Features](#features)
- [Installation](#installation)
- [OCPP in two minutes](#ocpp-in-two-minutes)
- [Usage](#usage)
  - [General CLI usage](#general-cli-usage)
  - [Connecting to a CSMS](#connecting-to-a-csms)
  - [Runbook](#runbook)
  - [Messages and shortcuts](#messages-and-shortcuts)
  - [Simulate a station](#simulate-a-station)
  - [Scenarios](#scenarios)
  - [Run a CSMS](#run-a-csms)
  - [Offline tools](#offline-tools)
- [Environment variable configuration](#environment-variable-configuration)
- [Known issues and open questions](#known-issues-and-open-questions)
- [FAQ](#faq)
- [Project structure](#project-structure)
- [Development](#development)
- [Version history](#version-history)
- [Contributing](#contributing)
- [License](#license)

---

## Features

| **Feature**      | **Description**                                                                                                           |
|------------------|---------------------------------------------------------------------------------------------------------------------------|
| Versions         | OCPP 1.6J (with the actions of the 1.6 Security Whitepaper that go-ocpp defines), 2.0.1 and 2.1, including the 2.1 SEND and CALLRESULTERROR. **`--ocpp-version`** lists the versions to offer; the CSMS picks one. |
| Security profiles | 0 (ws://), 1 (ws:// with HTTP Basic), 2 (TLS with HTTP Basic), 3 (TLS with client certificates), as client and as server. TLS 1.2 minimum, verification always on. |
| Probe            | Handshake report: negotiated version, TLS version, cipher suite and certificate, authentication, compression, handshake time; **`--each-version`** tries each version separately. |
| Call / send      | One CALL or SEND (2.1) with a payload from JSON, a file or stdin, validated before sending; the response validated after; **`--raw`** for invalid requests; **`--boot`** for CSMSs that want a BootNotification first. |
| Shortcuts        | BootNotification, Heartbeat, Authorize, StatusNotification, MeterValues, DataTransfer, Start/StopTransaction (1.6) and TransactionEvent Started/Ended (2.x), built for the negotiated version; **`--payload-only`** prints the payload. |
| Simulated station | **`client run`**: boot with Pending/Rejected handling, StatusNotifications, heartbeats, charging sessions with meter values, remote start/stop, Reset, TriggerMessage, GetConfiguration/ChangeConfiguration, GetVariables/SetVariables/GetBaseReport and more, answers from a YAML model, reconnection with the OCPP back-off, identity ranges for load tests. |
| Scenarios        | **`client script`**: calls with expectations (equality, regex, presence, one-of), waits for CALLs from the CSMS with answers, silence checks, variables, disconnect/reconnect; pass/fail per step and exit code 7. |
| CSMS             | **`server start`**: every version and security profile, passwords per station, client certificates, a model for boot status, tokens, transaction ids and per-action answers, an event stream; **`server call`** and **`server stations`** through a control socket. |
| Offline          | **`actions`**, **`validate`**, **`frame decode|encode`**, **`cert hash`**. |
| `--format`       | Every command that prints a result accepts **`--format text\|json\|table`** (or **`OCPPCTL_OUTPUT_FORMAT`**); streams write JSON Lines. |
| `--debug`        | Every frame sent and received, and go-ocpp's log, on stderr. |
| Completion       | Shell completion for bash, zsh, fish and PowerShell; flags with a fixed set of values (**`--format`**, **`--ocpp-version`**, **`--status`**, **`--reason`**, …) complete to those values. |

The following tree view gives the overview of available commands.

```
ocppctl
├── client                  act as a Charging Station
│   ├── probe               connect, report what the handshake negotiated, disconnect
│   ├── call <Action>       send one CALL, print the response
│   ├── send <Action>       send one SEND (OCPP 2.1)
│   ├── boot                BootNotification
│   ├── heartbeat           Heartbeat
│   ├── authorize           Authorize
│   ├── status              StatusNotification
│   ├── meter               MeterValues
│   ├── data-transfer       DataTransfer
│   ├── transaction
│   │   ├── start           StartTransaction (1.6) / TransactionEvent Started (2.x)
│   │   └── stop            StopTransaction (1.6) / TransactionEvent Ended (2.x)
│   ├── run                 run simulated stations from a model
│   ├── script <file>       run a scenario with expectations
│   └── generate-config     write the commented station model
├── server                  act as a CSMS
│   ├── start               accept stations, answer them from a model, stream events
│   ├── call <Action>       send a CALL to a connected station
│   ├── stations            list the connected stations
│   └── generate-config     write the commented CSMS model
├── actions                 list the actions of a version
├── validate <Action>       validate a payload against the OCA schema
├── frame
│   ├── decode              decode an OCPP-J frame
│   └── encode              build an OCPP-J frame
├── cert
│   └── hash                CertificateHashData of a certificate
├── completion              shell completion script (bash|zsh|fish|powershell)
└── version                 print version
```

## Installation

The easiest way to install ocppctl is to download a pre-built binary from the [GitHub Releases page](https://github.com/otfabric/ocppctl/releases). No Go toolchain is required.

| Platform | CPU | Download |
|---|---|---|
| Linux | x86-64 | `ocppctl-<version>-linux-amd64.tar.gz` |
| Linux | 64-bit ARM (Raspberry Pi 4/5 with a 64-bit OS, AWS Graviton) | `ocppctl-<version>-linux-arm64.tar.gz` |
| Linux | 32-bit ARM (Raspberry Pi with a 32-bit OS) | `ocppctl-<version>-linux-armv7.tar.gz` |
| macOS | Apple Silicon (M1 and later) | `ocppctl-<version>-darwin-arm64.tar.gz` |
| macOS | Intel | `ocppctl-<version>-darwin-amd64.tar.gz` |
| Windows | x86-64 | `ocppctl-<version>-windows-amd64.zip` |
| Windows | ARM64 | `ocppctl-<version>-windows-arm64.zip` |

Each release also contains `ocppctl-<version>-checksums.txt` (SHA-256) so you can verify your download. The commands below fetch the **latest** release, verify its checksum and install it. Afterwards, check that it works:

```console
ocppctl version
```

### Linux

```bash
# 1. Find the latest version and your CPU architecture
VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/otfabric/ocppctl/releases/latest | sed 's|.*/v||')
case "$(uname -m)" in
  x86_64)         ARCH=amd64 ;;
  aarch64|arm64)  ARCH=arm64 ;;
  armv7l)         ARCH=armv7 ;;
  *) echo "Unsupported architecture: $(uname -m)" ;;
esac

# 2. Download the archive and the checksum file
FILE="ocppctl-${VERSION}-linux-${ARCH}.tar.gz"
BASE="https://github.com/otfabric/ocppctl/releases/download/v${VERSION}"
curl -fsSLO "${BASE}/${FILE}"
curl -fsSLO "${BASE}/ocppctl-${VERSION}-checksums.txt"

# 3. Verify the download (should print "OK")
grep "${FILE}" "ocppctl-${VERSION}-checksums.txt" | sha256sum -c -

# 4. Extract and install (the binary in the archive carries the version and platform in its name)
mkdir -p ocppctl-dl && tar -xzf "${FILE}" -C ocppctl-dl
sudo install -m 0755 "$(find ocppctl-dl -type f -name 'ocppctl*' | head -n1)" /usr/local/bin/ocppctl
```

No `sudo`? Install into your home directory instead (and make sure `~/.local/bin` is in your `PATH`):

```bash
mkdir -p ~/.local/bin
install -m 0755 "$(find ocppctl-dl -type f -name 'ocppctl*' | head -n1)" ~/.local/bin/ocppctl
```

### macOS

```bash
# 1. Find the latest version and your CPU architecture (arm64 = Apple Silicon, x86_64 = Intel)
VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/otfabric/ocppctl/releases/latest | sed 's|.*/v||')
case "$(uname -m)" in
  arm64)   ARCH=arm64 ;;
  x86_64)  ARCH=amd64 ;;
esac

# 2. Download the archive and the checksum file
FILE="ocppctl-${VERSION}-darwin-${ARCH}.tar.gz"
BASE="https://github.com/otfabric/ocppctl/releases/download/v${VERSION}"
curl -fsSLO "${BASE}/${FILE}"
curl -fsSLO "${BASE}/ocppctl-${VERSION}-checksums.txt"

# 3. Verify the download (should print "OK")
grep "${FILE}" "ocppctl-${VERSION}-checksums.txt" | shasum -a 256 -c -

# 4. Extract and install (the binary in the archive carries the version and platform in its name)
mkdir -p ocppctl-dl && tar -xzf "${FILE}" -C ocppctl-dl
sudo install -m 0755 "$(find ocppctl-dl -type f -name 'ocppctl*' | head -n1)" /usr/local/bin/ocppctl
```

If you downloaded the archive with a web browser instead of `curl` and macOS refuses to run the binary ("cannot be verified"), remove the quarantine flag: `xattr -d com.apple.quarantine /usr/local/bin/ocppctl`.

### Windows (PowerShell)

```powershell
# 1. Find the latest version and your CPU architecture
$version = (Invoke-RestMethod https://api.github.com/repos/otfabric/ocppctl/releases/latest).tag_name.TrimStart('v')
$arch    = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

# 2. Download the archive and the checksum file
$file = "ocppctl-$version-windows-$arch.zip"
$base = "https://github.com/otfabric/ocppctl/releases/download/v$version"
Invoke-WebRequest "$base/$file" -OutFile "$env:TEMP\$file"
Invoke-WebRequest "$base/ocppctl-$version-checksums.txt" -OutFile "$env:TEMP\ocppctl-checksums.txt"

# 3. Verify the download (throws an error on mismatch)
$expected = ((Select-String -Path "$env:TEMP\ocppctl-checksums.txt" -Pattern $file).Line -split '\s+')[0]
$actual   = (Get-FileHash "$env:TEMP\$file" -Algorithm SHA256).Hash
if ($expected -ne $actual) { throw "Checksum mismatch for $file" }

# 4. Extract to a per-user folder and name the program ocppctl.exe
#    (the .exe in the archive carries the version and platform in its name)
$dir = "$env:LOCALAPPDATA\Programs\ocppctl"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Expand-Archive "$env:TEMP\$file" -DestinationPath "$env:TEMP\ocppctl-dl" -Force
$exe = Get-ChildItem "$env:TEMP\ocppctl-dl" -Recurse -Filter 'ocppctl*.exe' | Select-Object -First 1
Copy-Item $exe.FullName "$dir\ocppctl.exe" -Force

# 5. Add ocppctl to your user PATH (then open a new terminal)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$dir*") {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
}
```

If Windows SmartScreen warns about an unrecognised app, choose **More info → Run anyway**.

### Manual download

1. Open the [latest release](https://github.com/otfabric/ocppctl/releases/latest).
2. Download the archive for your platform from the table above.
3. Extract it, rename the binary to `ocppctl` (`ocppctl.exe` on Windows) and move it to a folder on your `PATH`.

### Update

Run the same commands again. They always fetch the latest release and overwrite the previous binary.

### Build from source

Requires Go 1.26 or later:

```console
git clone https://github.com/otfabric/ocppctl.git
cd ocppctl
make build
```

`make build` runs the checks and writes the binary to **`bin/ocppctl`** (see [Building](#building)). With a Go toolchain you can also install straight from the module:

```console
go install github.com/otfabric/ocppctl@latest
```

---

## OCPP in two minutes

OCPP-J carries JSON messages over a WebSocket. The **Charging Station** (the client) opens the connection to the **CSMS**, the Charging Station Management System (the server; in OCPP 1.6 the *Central System*). Once it is open, either side sends requests to the other.

| Term | Meaning | In ocppctl |
|---|---|---|
| Endpoint and identity | The station connects to the CSMS's URL with its own identity as the last path segment: `wss://csms.example.com/ocpp/CS001`. The identity is how the CSMS knows the station | `--url` |
| Version | The station offers WebSocket subprotocols (`ocpp2.1`, `ocpp2.0.1`, `ocpp1.6`); the CSMS picks one, and the session speaks that version. 1.6 and 2.x differ in almost every message | `--ocpp-version`, `client probe` |
| Security profile | 0: ws://, no authentication. 1: ws:// with an HTTP Basic password. 2: TLS with an HTTP Basic password. 3: TLS with a client certificate. The Basic user name is the identity | `--password-file`, `--tls-*` |
| CALL / CALLRESULT / CALLERROR | A request, its answer, or an error answer (`NotSupported`, `FormatViolation`, …). Every CALL is answered by exactly one of the two | `client call`, `server call` |
| SEND, CALLRESULTERROR | OCPP 2.1 only: a message that is never answered, and an error about an answer | `client send`, `frame` |
| Action | The name of a message: `BootNotification`, `Heartbeat`, `StatusNotification`, `TransactionEvent`, … Each is sent by the station or by the CSMS (DataTransfer by both) | `actions` |
| Schema | The OCA publishes a JSON schema for every request and response of every version. A message that does not conform is answered with a CALLERROR | `validate` |
| Boot | A station first sends a BootNotification; the CSMS answers Accepted (with the heartbeat interval), Pending or Rejected. Many CSMSs refuse other messages before Accepted | `client boot`, `--boot` |
| EVSE and connector | In 2.x a station has EVSEs (charging points that can deliver energy at once) with connectors. In 1.6 there are only connectors, numbered across the station; 0 is the station itself | `--evse`, `--connector` |
| Id token | What starts a session: an RFID UID, an eMAID, … `idTag` (a string) in 1.6, `idToken` (with a type) in 2.x | `--id-token` |
| Transaction | A charging session. In 1.6 the CSMS assigns an integer id in its StartTransaction answer; in 2.x the station chooses a string id and reports the session with TransactionEvent Started, Updated and Ended | `client transaction` |
| Configuration | 1.6 stations have configuration keys (GetConfiguration, ChangeConfiguration); 2.x stations a device model of components and variables (GetVariables, SetVariables, GetBaseReport) | the station model |

---

## Usage

### General CLI Usage

```console
ocppctl [command] [flags]

ocppctl client probe
ocppctl client call <Action>
ocppctl client send <Action>
ocppctl client boot | heartbeat | authorize | status | meter | data-transfer
ocppctl client transaction start | stop
ocppctl client run
ocppctl client script <scenario.yaml>
ocppctl client generate-config

ocppctl server start
ocppctl server call <Action>
ocppctl server stations
ocppctl server generate-config

ocppctl actions
ocppctl validate <Action>
ocppctl frame decode | encode
ocppctl cert hash
ocppctl version
```

Every command has `--help`, with a description and examples. Action names are case-sensitive, as in OCPP (`BootNotification`); a near miss is suggested.

**Global flag:** `--debug` — trace every frame sent and received, and go-ocpp's log, to stderr, and print the chain of causes after an error. Frames, not handshake headers: credentials are never traced.

**Output format (`--format`):** every command that prints a result accepts **`--format text`** (default), **`json`** or **`table`**, or set **`OCPPCTL_OUTPUT_FORMAT`**. It controls **only** the result on **stdout**. Progress (`connected to … as CS001 (OCPP 2.1)`), summaries and the debug trace go to **stderr**, so stdout can be piped or parsed as it is. The JSON shapes are a stable contract: see [docs/json-output.md](docs/json-output.md).

**Streams:** `client run` and `server start` write one record per event: one line (**text**), one JSON object per line (**json**, JSON Lines) or one table row (**table**). They run until interrupted with Ctrl-C, which ends them normally, or for `--duration`.

**Fatal errors:** with `--format json` a command writes a **single JSON error object** to stdout on failure (`error` with `kind`, `code`, `message`, and `ocpp` with protocol detail: the CALLERROR code, the field that failed the schema, the HTTP status of a refused handshake); with **text** or **table** it writes a single **`error:`** line to stderr and leaves stdout empty. Exit codes are stable:

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Usage: unknown command or flag, missing required flag; `server call` of a state-changing action without `--yes` |
| 3 | Invalid input: bad flag, payload, model or scenario; nothing was sent |
| 4 | Transport: cannot connect, TLS failure, handshake refused, no common OCPP version, connection lost |
| 5 | Timeout: no response (`outcome_unknown` when the request may have changed state) |
| 6 | The peer answered with a CALLERROR, an invalid response, or did not accept the boot |
| 7 | Partial success: a valid result that holds failures (failed scenario steps, failed stations) |
| 10 | Output or internal error, or interrupted |

See [docs/exitcodes.md](docs/exitcodes.md) for the details and the `code` values.

**Shell completion:** `ocppctl completion bash` (or `zsh`, `fish`, `powershell`) prints a completion script; for example `source <(ocppctl completion bash)`.

### Connecting to a CSMS

Every `client` command takes the CSMS endpoint followed by the station identity as `--url`:

```
ws[s]://host[:port]/path/<identity>
```

| Flag | Default | Meaning |
|---|---|---|
| `--url` | | the endpoint and the identity. Credentials do not go in the URL |
| `--ocpp-version` | `2.1,2.0.1,1.6` | the versions to offer, in order of preference; the CSMS picks one. Give one to force it |
| `--password-file` | | a file holding the HTTP Basic password (profiles 1 and 2); or set `OCPPCTL_PASSWORD`. The user name is the identity |
| `--tls-ca` | system roots | PEM file of the CAs that verify the CSMS certificate (for a private or test CA) |
| `--tls-cert`, `--tls-key` | | the station's client certificate and key (profile 3) |
| `--tls-server-name` | the URL host | the name to verify the CSMS certificate against |
| `--insecure-transport` | off | allow `ws://` (profiles 0 and 1): no encryption, the password in clear text |
| `--compression` | off | offer permessage-deflate |
| `--header NAME=VALUE` | | an extra handshake header (repeatable) |
| `--timeout` | `30s` | the response timeout of a CALL |
| `--connect-timeout` | `10s` | dial, TLS and WebSocket handshake |

| Profile | Transport | Flags |
|---|---|---|
| 0 | ws://, no password | `--insecure-transport` |
| 1 | ws://, HTTP Basic | `--insecure-transport` and `--password-file` (or `OCPPCTL_PASSWORD`) |
| 2 | wss://, HTTP Basic | `--password-file` (or `OCPPCTL_PASSWORD`), and `--tls-ca` for a private CA |
| 3 | wss://, client certificate | `--tls-cert` and `--tls-key`, and `--tls-ca` for a private CA |

There is no way to skip the verification of the CSMS certificate: trust a test CA with `--tls-ca`. A `ws://` URL without `--insecure-transport` is refused before connecting (exit 3).

### Runbook

#### Is my CSMS reachable, and which version does it pick?

```console
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

`probe` sends no OCPP message: it connects, reports and disconnects. Every version the CSMS accepts, one connection each:

```console
$ ocppctl client probe --url ws://csms.local:9000/CS001 --insecure-transport --password-file pw.txt --each-version --format table
OFFERED  RESULT             VERSION  TLS  AUTH   COMPRESSION  HANDSHAKE_MS
-        -                  -        -    -      -            -
2.1      no_common_version  -        -    basic  false        0
2.0.1    accepted           2.0.1    -    basic  false        0
1.6      accepted           1.6      -    basic  false        0
```

Security profile 2 (TLS and a password) against a private CA, as JSON:

```console
ocppctl client probe --url wss://csms.test:8443/ocpp/CS002 --tls-ca ca.pem --password-file pw.txt --format json
```

A refusal says why:

```console
$ ocppctl client probe --url ws://csms.local:9000/CS001 --insecure-transport
error: connection refused by the CSMS: HTTP 401: authentication failed: wrong password, or no credentials (security profiles 1 and 2)
$ ocppctl client probe --url ws://csms.local:9000/CS999 --insecure-transport --password-file pw.txt
error: connection refused by the CSMS: HTTP 404: unknown station identity, or wrong endpoint path
```

With `--format json` the HTTP status is in `.ocpp.http_status`. An untrusted certificate is `transport_tls_failed`, a CSMS that speaks none of the offered versions `no_common_version`; all exit 4.

#### Register a station and check the boot status

```console
$ ocppctl client boot --url ws://localhost:9000/CS001 --insecure-transport --vendor OTFabric --model Sim --serial SIM-0001
connected to ws://localhost:9000/CS001 as CS001 (OCPP 2.1)
BootNotification answered (OCPP 2.1, 6 ms)
{
  "currentTime": "2026-10-18T20:48:54.108Z",
  "interval": 300,
  "status": "Accepted"
}
```

The command succeeds whenever the CSMS answers validly, whatever the status: check it in a script with

```console
ocppctl client boot --url wss://csms.example.com/ocpp/CS001 --format json | jq -r .response.status
```

Any other command can boot first with `--boot` (vendor and model from `--boot-vendor` and `--boot-model`): it then fails with exit 6 (`boot_not_accepted`, the status in `.ocpp.status`) when the boot is Pending or Rejected.

#### Drive a charging session by hand

OCPP 1.6: the CSMS assigns the transaction id, so keep it:

```console
U="--url ws://localhost:9000/CS001 --insecure-transport --ocpp-version 1.6"
ocppctl client boot $U
ocppctl client authorize $U --id-token 04A1B2C3D4E5F6
ocppctl client status $U --connector 1 --status Preparing
TX=$(ocppctl client transaction start $U --connector 1 --id-token 04A1B2C3D4E5F6 --format json | jq -r .response.transactionId)
ocppctl client status $U --connector 1 --status Charging
ocppctl client meter $U --connector 1 --energy-wh 7400 --power-w 11000 --transaction "$TX"
ocppctl client transaction stop $U --transaction "$TX" --energy-wh 15300 --reason Local
ocppctl client status $U --connector 1 --status Available
```

OCPP 2.0.1: the station chooses the transaction id (`transaction start` makes one up without `--transaction`, and prints it in `.request.transactionInfo.transactionId`):

```console
U="--url ws://localhost:9000/CS001 --insecure-transport --ocpp-version 2.0.1"
ocppctl client boot $U
ocppctl client status $U --evse 1 --connector 1 --status Occupied
ocppctl client transaction start $U --evse 1 --connector 1 --id-token 04A1B2C3D4E5F6 --transaction tx-0001
ocppctl client meter $U --evse 1 --energy-wh 7400 --power-w 11000
ocppctl client transaction stop $U --evse 1 --transaction tx-0001 --energy-wh 15300 --reason Local --seq-no 1
ocppctl client status $U --evse 1 --connector 1 --status Available
```

Each command is its own connection. A CSMS that wants the BootNotification on the same connection gets it with `--boot`.

#### Send any message

```console
# Inline JSON
ocppctl client call DataTransfer --url "$URL" --payload '{"vendorId":"com.example","messageId":"Ping"}'

# From a file, or from stdin
ocppctl client call StatusNotification --url "$URL" --boot --payload @status.json
cat request.json | ocppctl client call MeterValues --url "$URL" --payload -

# A shortcut as a payload builder: build, edit, send
ocppctl client meter --ocpp-version 1.6 --connector 1 --energy-wh 1500 --payload-only |
  jq '.meterValue[0].sampledValue[0].context = "Sample.Clock"' |
  ocppctl client call MeterValues --url "$URL" --ocpp-version 1.6 --payload -
```

The payload is validated against the schema of the version the CSMS chose before it is sent; with a single `--ocpp-version`, before connecting. An invalid one exits 3 and nothing is sent:

```console
$ ocppctl client call BootNotification --url "$URL" --ocpp-version 2.0.1 --payload '{"reason":"PowerUp"}' --format json
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

`--raw` sends it anyway, to see what the CSMS does; the response is still validated. A CALLERROR answer exits 6 with the code in `.ocpp.error_code`:

```console
$ ocppctl client call Heartbeat --url "$URL" --raw --payload '{"bogus":1}'
connected to ws://localhost:9000/CS001 as CS001 (OCPP 2.1)
error: Heartbeat: the peer answered with CALLERROR FormatViolation: bogus: not a property of this object
```

A CALL that may change state and gets no answer in `--timeout` exits 5 with `outcome_unknown`: the CSMS may have acted on it, and ocppctl does not send it again. CALLs the CSMS sends while ocppctl waits are answered with the first schema-valid answer (`{}` or status Accepted) and listed in `.received`. For a SEND (OCPP 2.1): `ocppctl client send NotifyPeriodicEventStream --url "$URL" --payload @stream.json`.

#### Simulate a station, or 100 stations for a load test

```console
# One station from the built-in model, against a local CSMS
ocppctl client run --url ws://localhost:9000/CS001 --insecure-transport

# Your model, OCPP 2.0.1 only, for ten minutes, as JSON Lines
ocppctl client generate-config --output station.yaml
ocppctl client run --url wss://csms.example.com/ocpp/CS001 --ocpp-version 2.0.1 \
  --config station.yaml --duration 10m --format json > events.jsonl

# 100 stations, 50 ms apart, for 15 minutes
ocppctl client run --url 'wss://csms.example.com/ocpp/LOAD{001..100}' --stagger 50ms --duration 15m --format json > load.jsonl
```

At the end of a multi-station run a summary goes to stderr:

```
summary: 3 stations, 3 booted, 0 failed; 9 calls, 0 errors, latency avg 0s max 1ms; 0 sessions
```

The exit code is 7 when some stations failed (`failed: CS002` on stderr) and others ran. Count events per type, or follow the sessions:

```console
jq -r .event load.jsonl | sort | uniq -c
jq -c 'select(.event | startswith("session_"))' load.jsonl
```

#### Regression-test a CSMS in CI

```console
ocppctl client script examples/scenarios/smoke-2.0.1.yaml --url ws://csms:9000/CI001 --insecure-transport \
  --var token=04A1B2C3D4E5F6 --keep-going --format json > result.json
```

Exit 0: every step passed; 7: some failed (`jq '.steps[] | select(.result=="fail")' result.json` says which and why); 3: the scenario is invalid; 4: the CSMS cannot be reached. A GitHub Actions step:

```yaml
- name: OCPP regression against the staging CSMS
  env:
    OCPPCTL_PASSWORD: ${{ secrets.CSMS_TEST_PASSWORD }}
  run: |
    ocppctl client script tests/ocpp/remote-start.yaml \
      --url wss://csms-staging.example.com/ocpp/CI-${{ github.run_id }} \
      --keep-going --format json > ocpp-result.json
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: ocpp-result
    path: ocpp-result.json
```

See [Scenarios](#scenarios) and [docs/scenarios.md](docs/scenarios.md).

#### A local CSMS for station development

```console
# Terminal 1: every version, no TLS, no authentication, events as JSON Lines
ocppctl server start --insecure-transport --no-auth --format json | tee events.jsonl

# Terminal 2: point the station at ws://<this host>:9000/<identity>, then
ocppctl server stations
ocppctl server call Reset --station CS001 --yes --payload '{"type":"Immediate"}'                  # 2.x
ocppctl server call Reset --station CP16 --yes --payload '{"type":"Soft"}'                        # 1.6
ocppctl server call RequestStartTransaction --station CS001 --yes \
  --payload '{"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}'
ocppctl server call RemoteStartTransaction --station CP16 --yes --payload '{"idTag":"TESTTOKEN","connectorId":1}'
ocppctl server call GetVariables --station CS001 \
  --payload '{"getVariableData":[{"component":{"name":"OCPPCommCtrlr"},"variable":{"name":"HeartbeatInterval"}}]}'
```

```console
$ ocppctl server stations
CP16                 OCPP 1.6    127.0.0.1:62656        connected 2026-10-18T20:49:35.361Z  tls=false compression=false
CS201                OCPP 2.0.1  127.0.0.1:62657        connected 2026-10-18T20:49:35.361Z  tls=false compression=false
```

A CALL that may change the station needs `--yes` (exit 2 without it); read-only ones such as GetVariables and GetConfiguration do not. Follow the station with jq:

```console
# Every CALL a station sends, compactly
jq -c 'select(.event=="call_received") | {identity, action, request}' events.jsonl

# Transactions as the CSMS answers them
jq -c 'select(.event=="answered" and (.action=="StartTransaction" or .action=="TransactionEvent")) | {identity, action, response}' events.jsonl
```

#### Decode a frame from a log

```console
$ ocppctl frame decode '[2,"19223201","BootNotification",{"chargePointVendor":"VendorX","chargePointModel":"SingleSocketCharger"}]' --ocpp-version 1.6
type        CALL (2)
message id  19223201
action      BootNotification
payload     valid for OCPP 1.6
{
  "chargePointModel": "SingleSocketCharger",
  "chargePointVendor": "VendorX"
}
frame       [2,"19223201","BootNotification",{"chargePointVendor":"VendorX","chargePointModel":"SingleSocketCharger"}]
```

A frame on stdin: `pbpaste | ocppctl frame decode - --ocpp-version 2.0.1 --format json`. The payload of a CALL or SEND is checked against its schema (`payload_valid`, `violation`) without failing the command; a CALLRESULT carries no action, so validate its payload with `ocppctl validate <Action> --response`. A malformed frame exits 3.

#### Certificate hash for DeleteCertificate

```console
$ ocppctl cert hash --cert cs001.pem --issuer ca.pem
certificate       CN=CS001
hash algorithm    SHA256
issuer name hash  54590a6c71a7f18d9accd2f940aa89bcf39f88e71e54e364d683d4c7cbc22f93
issuer key hash   a90fdc70d833d67639d304a5cb5ef9cd49f988088c7cd541e31ece6894e423b0
serial number     71b1cab1e036d0431cdee2d27cffbca47d7ae06d
```

A root is its own issuer, so `--issuer` can be left out. The `ocpp` object of the JSON output is the `certificateHashData` as messages carry it:

```console
ocppctl cert hash --cert root.pem --format json | jq -c '{certificateHashData: .ocpp}' |
  ocppctl server call DeleteCertificate --station CS001 --yes --payload -
```

ocppctl checks that `--cert` is signed by `--issuer`: a hash over the wrong issuer would name a different certificate.

### Messages and shortcuts

`client call <Action>` sends any CALL a station sends in the negotiated version; `client send <Action>` any SEND (it offers only 2.1 by default). Both take `--payload` (JSON, `@file`, or `-` for stdin; default `{}`), `--raw` and `--boot`.

The shortcuts build the payload from flags for the version the CSMS picks, validate it, and send it like `client call`; the result is the same. `--payload-only` prints the payload for the one `--ocpp-version` given and connects to nothing.

| Command | Sends | Flags (defaults) |
|---|---|---|
| `client boot` | BootNotification | `--vendor` (OTFabric), `--model` (ocppctl), `--serial`, `--firmware`, `--reason` (PowerUp; 2.x) |
| `client heartbeat` | Heartbeat | |
| `client authorize` | Authorize | `--id-token` (required), `--type` (ISO14443; 2.x) |
| `client status` | StatusNotification | `--status` (required), `--connector` (1), `--evse` (1; 2.x), `--error-code` (NoError; 1.6) |
| `client meter` | MeterValues: `Energy.Active.Import.Register` and, with `--power-w`, `Power.Active.Import` | `--energy-wh` (required), `--power-w`, `--connector` (1; 1.6), `--evse` (1; 2.x), `--transaction` (1.6, an integer) |
| `client data-transfer` | DataTransfer | `--vendor` (required), `--message-id`, `--data` (2.x: JSON when it parses as JSON) |
| `client transaction start` | StartTransaction (1.6), TransactionEvent Started (2.x) | `--id-token` (required), `--connector` (1), `--evse` (1; 2.x), `--type` (ISO14443; 2.x), `--meter-start-wh`, `--transaction` (2.x; default: a random id) |
| `client transaction stop` | StopTransaction (1.6), TransactionEvent Ended (2.x) | `--transaction` (required), `--energy-wh`, `--reason`, `--evse`, `--connector` (1; 2.x), `--seq-no` (1; 2.x) |

1.6 statuses: Available, Preparing, Charging, SuspendedEVSE, SuspendedEV, Finishing, Reserved, Unavailable, Faulted. 2.x statuses: Available, Occupied, Reserved, Unavailable, Faulted.

### Simulate a station

`client run` connects a simulated station described by a model and keeps it running:

- it sends BootNotification until Accepted (waiting the CSMS's interval while Pending or Rejected), a StatusNotification for every connector, then Heartbeat at the CSMS's interval;
- it charges the sessions of the model: StartTransaction, MeterValues and StopTransaction in 1.6, TransactionEvent Started, Updated and Ended in 2.x;
- it answers the CSMS from the model's `answers`, then with built-in behaviours (RemoteStart/StopTransaction and RequestStart/StopTransaction start and stop a session; Reset ends the sessions, reconnects and boots with reason RemoteReset; TriggerMessage sends the message; GetConfiguration/ChangeConfiguration, GetVariables/SetVariables and GetBaseReport work on the model's configuration and device model; …), then with the first schema-valid answer;
- it reconnects with the OCPP back-off when the connection is lost, unless `--no-reconnect`.

```yaml
station: {vendor: OTFabric, model: Simulator, serial: SIM-0001, firmware: 1.0.0}
evses:
  - {id: 1, connectors: [1]}
  - {id: 2, connectors: [1]}
meter_interval: 60s
sessions:
  - {after: 30s, evse: 1, connector: 1, id_token: 04A1B2C3D4E5F6, power_w: 11000, duration: 5m}
device_model:
  OCPPCommCtrlr.HeartbeatInterval: {value: "300", mutability: ReadWrite}
configuration:
  HeartbeatInterval: {value: "300"}
answers:
  UnlockConnector: {status: Unlocked}
```

Every event is written to stdout:

```console
20:49:35.361 CS201 connected tls=false compression=false
20:49:35.362 CS201 call_sent BootNotification {"chargingStation":{"firmwareVersion":"1.0.0","model":"Simulator","serialNumber":"SIM-0001","vendorName":"OTFabric"},"reason":"PowerUp"}
20:49:35.362 CS201 call_result BootNotification 0 ms {"currentTime":"2026-10-18T20:49:35.362Z","interval":300,"status":"Accepted"}
20:49:35.362 CS201 booted heartbeat every 5m0s
20:49:44.660 CS201 call_received RequestStartTransaction {"remoteStartId":1,"idToken":{"idToken":"TESTTOKEN","type":"ISO14443"}}
20:49:44.661 CS201 answered RequestStartTransaction {"status":"Accepted","transactionId":"0445ef4c-9e0a-80d0-5595-c06a9774b839"}
20:49:44.662 CS201 session_started transaction 0445ef4c-9e0a-80d0-5595-c06a9774b839 EVSE 1 connector 1, token TESTTOKEN, Accepted
20:49:45.723 CS201 call_received Reset {"type":"Immediate"}
20:49:45.723 CS201 reset Reset by the CSMS: ending sessions and rebooting
```

| Flag | Meaning |
|---|---|
| `--config` | the model (YAML or JSON); the built-in one without it |
| `--duration` | stop after this long (`0`: until Ctrl-C) |
| `--no-reconnect` | exit 4 when the connection is lost |
| `--ping-interval` | WebSocket ping interval (`0`: none) |
| `--stagger` | with a `{N..M}` identity range: delay between station starts |

The full reference of the model, every built-in behaviour and the events is in [docs/station-model.md](docs/station-model.md).

### Scenarios

`client script` runs a scenario against a CSMS:

```yaml
name: remote-start-2.0.1
version: "2.0.1"
steps:
  - call: BootNotification
    payload: {reason: PowerUp, chargingStation: {vendorName: OTFabric, model: Simulator}}
    expect: {status: Accepted}
  - name: remote start
    wait_for: RequestStartTransaction      # a CALL from the CSMS
    timeout: 60s
    expect:
      idToken.idToken: {present: true}
    answer: {status: Accepted}
    save: {token: idToken.idToken, remote: remoteStartId}
  - call: TransactionEvent
    payload:
      eventType: Started
      timestamp: "${now}"
      triggerReason: RemoteStart
      seqNo: 0
      transactionInfo: {transactionId: "${uuid}", chargingState: Charging, remoteStartId: "${remote}"}
      evse: {id: 1, connectorId: 1}
      idToken: {idToken: "${token}", type: ISO14443}
  - expect_no_call: 2s                     # the CSMS stays silent
```

Steps are `call`, `send`, `wait_for`, `expect_no_call`, `sleep`, `disconnect` and `reconnect`; expectations match fields by path (`getVariableResult[0].attributeStatus`) with equality, `regex`, `present` or `one_of`; `save` keeps a field for later payloads; `${now}`, `${uuid}`, `${identity}` and `--var NAME=VALUE` are variables; payloads can come from files (`"@payload.json"`, quoted). The result lists every step:

```console
$ ocppctl client script regression.yaml --url ws://localhost:9000/CI001 --insecure-transport --keep-going
scenario regression against ws://localhost:9000/CI001 (OCPP 2.0.1)
FAIL     1  call BootNotification (0 ms): interval is 300, want one of [60,120]
PASS     2  call Heartbeat (0 ms)
FAIL     3  call DataTransfer (0 ms): answered with a CALLRESULT, want CALLERROR NotSupported
FAIL     4  no remote start: wait_for RequestStartTransaction (1002 ms): no RequestStartTransaction from the CSMS within 1s
1 passed, 3 failed, 0 skipped in 1005 ms
```

Three examples are in [examples/scenarios](examples/scenarios): a 1.6 and a 2.0.1 charging session and a remote start. The format is described in [docs/scenarios.md](docs/scenarios.md).

### Run a CSMS

```console
# A local CSMS: no TLS, no authentication, every version
ocppctl server start --insecure-transport --no-auth

# Write the model, edit it, serve it
ocppctl server generate-config --output csms.yaml
ocppctl server start --config csms.yaml --insecure-transport --no-auth

# Security profile 2: TLS and passwords from a file of identity:password lines
ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key --passwords-file passwords.txt

# Security profile 3: client certificates of this CA (identity = certificate CN)
ocppctl server start --listen :8443 --tls-cert server.pem --tls-key server.key --tls-client-ca station-ca.pem
```

The server answers BootNotification from the model (Accepted, Pending or Rejected, with an interval), Heartbeat with the time, Authorize and transactions against a token list, 1.6 StartTransaction with increasing transaction ids, DataTransfer with UnknownVendorId, the notifications with an empty answer, and lets the model override any of it per action, version and station. Every message is validated in both directions. Every event is written to stdout:

```console
2026-10-18T20:49:21.771Z  listening      address=127.0.0.1:19192 versions=2.0.1,1.6
2026-10-18T20:49:22.809Z  refused        CS001  127.0.0.1:62542  reason="no credentials (HTTP 401)"
2026-10-18T20:49:22.852Z  refused        CS999  127.0.0.1:62544  reason="unknown station identity (HTTP 404)"
2026-10-18T20:49:22.886Z  connected      CS002  2.0.1  127.0.0.1:62549  tls=false compression=false
2026-10-18T20:49:22.886Z  call_received  CS002  2.0.1  BootNotification  request={"chargingStation":{"model":"ocppctl","vendorName":"OTFabric"},"reason":"PowerUp"}
2026-10-18T20:49:22.886Z  answered       CS002  2.0.1  BootNotification  response={"currentTime":"2026-10-18T20:49:22.886Z","interval":300,"status":"Pending"}
2026-10-18T20:49:22.887Z  disconnected   CS002  2.0.1  127.0.0.1:62549  reason="ocpp: connection lost: closed by peer with status 1000"
```

| Flag | Meaning |
|---|---|
| `--listen` | host:port (default `:9000`, all interfaces) |
| `--path` | URL path prefix (default `/`): stations connect to `<path><identity>` |
| `--ocpp-version` | versions accepted (default `2.1,2.0.1,1.6`) |
| `--config` | the model; the built-in one without it |
| `--passwords-file`, `--no-auth` | station passwords, or no authentication; one of them (or `--tls-client-ca`) is required |
| `--tls-cert`, `--tls-key`, `--tls-client-ca` | TLS, and client certificates |
| `--insecure-transport` | allow listening without TLS |
| `--control-socket` | the Unix socket of `server call` and `server stations` (default `ocppctl.sock` in the working directory; `''` disables it) |
| `--max-stations`, `--ping-interval`, `--rate-limit`, `--rate-burst`, `--allow` | limits, keepalive, allowed addresses |
| `--duration` | stop after this long |

`server stations` lists the connected stations; `server call <Action> --station ID` sends one a CALL, with `--yes` for anything that may change it. Both find the server through the control socket, so run them from the same directory or set `OCPPCTL_CONTROL_SOCKET`. The model, the events, the control socket protocol and a runbook are in [docs/server-config.md](docs/server-config.md). The server listens on all interfaces by default: see [SECURITY.md](SECURITY.md).

### Offline tools

```console
# The actions of a version, and who sends them
ocppctl actions --ocpp-version 2.0.1 --format table
ocppctl actions --ocpp-version 1.6 --from csms
ocppctl actions --ocpp-version 2.1 --format json | jq -r '.actions[] | select(.kind=="send") | .name'

# Validate a request or a response
ocppctl validate BootNotification --ocpp-version 2.0.1 \
  --payload '{"reason":"PowerUp","chargingStation":{"vendorName":"OTFabric","model":"X"}}'
ocppctl validate BootNotification --ocpp-version 1.6 --response --payload @boot-response.json

# Build a frame
ocppctl frame encode --type call --id 1 --action Heartbeat --ocpp-version 1.6
ocppctl frame encode --type callerror --id 1 --error-code NotImplemented --description "Unknown action" --ocpp-version 2.0.1
```

```console
$ ocppctl validate BootNotification --ocpp-version 2.0.1 --payload '{"reason":"Booted","chargingStation":{"vendorName":"OTFabric","model":"X"}}'
invalid: BootNotification request of OCPP 2.0.1
  reason: "Booted" is not one of the allowed values (PropertyConstraintViolation)
```

`validate` exits 0 for a valid payload and 3 for an invalid one, with the result on stdout in both cases. The code in brackets is the CALLERROR a peer would answer with. `actions`, `validate` and `frame` take one `--ocpp-version` (default `2.0.1`).

---

## Environment Variable Configuration

Many flags can also be set through an environment variable, which is convenient for automation and for flags you would otherwise repeat on every command (`OCPPCTL_URL`, `OCPPCTL_PASSWORD`, `OCPPCTL_OUTPUT_FORMAT`). A flag on the command line wins over the environment; `--help` shows the variable of each flag as `[env: …]`. A value that cannot be parsed (a number that is not one, a boolean other than `true`/`false`/`1`/`0`) stops ocppctl at startup with exit 3. Lists are comma-separated. A flag that is **required** (`--id-token` of `client authorize`, `--station` of `server call`, …) must still be given on the command line, except `--url`, which `OCPPCTL_URL` fills.

```console
export OCPPCTL_URL=wss://csms.example.com/ocpp/CS001
export OCPPCTL_PASSWORD_FILE=$HOME/.config/ocppctl/cs001.pw
ocppctl client probe
ocppctl client boot
ocppctl client heartbeat --boot
```

Some variables are shared by the client and the server, with a meaning for each: `OCPPCTL_CONFIG` is the station model of `client run` and the CSMS model of `server start`, `OCPPCTL_TLS_CERT` / `OCPPCTL_TLS_KEY` are the station's client certificate and the server's certificate. `OCPPCTL_OCPP_VERSION` is a list for the client and the server, but one version for `actions`, `validate` and `frame`, which fail with a list. Set them per command when you use both roles from one shell.

<details>
<summary>All <code>OCPPCTL_*</code> environment variables</summary>

| Variable | Type | Description | Flag | Default |
|----------|------|-------------|------|---------|
| **OCPPCTL_ALLOW** | list | **`server start`**: accept connections only from these addresses or CIDR prefixes | `--allow` | any |
| **OCPPCTL_BOOT** | bool | **`client`** one-shot commands: send a BootNotification first and require Accepted | `--boot` | false |
| **OCPPCTL_BOOT_MODEL** | string | Model of the `--boot` BootNotification | `--boot-model` | ocppctl |
| **OCPPCTL_BOOT_VENDOR** | string | Vendor of the `--boot` BootNotification | `--boot-vendor` | OTFabric |
| **OCPPCTL_COMPRESSION** | bool | **`client`**: offer permessage-deflate | `--compression` | false |
| **OCPPCTL_CONFIG** | string | **`client run`**: station model; **`server start`**: CSMS model (YAML or JSON) | `--config` | built-in model |
| **OCPPCTL_CONNECT_TIMEOUT** | duration | **`client`**: dial, TLS and WebSocket handshake timeout | `--connect-timeout` | 10s |
| **OCPPCTL_CONTROL_SOCKET** | string | **`server start`**, **`call`**, **`stations`**: the control socket | `--control-socket` | ocppctl.sock |
| **OCPPCTL_DURATION** | duration | **`client run`**, **`server start`**: stop after this long | `--duration` | until interrupted |
| **OCPPCTL_INSECURE_TRANSPORT** | bool | **`client`**: allow ws://; **`server start`**: allow listening without TLS | `--insecure-transport` | false |
| **OCPPCTL_LISTEN** | string | **`server start`**: host:port to listen on | `--listen` | :9000 |
| **OCPPCTL_MAX_STATIONS** | int | **`server start`**: most stations at once (0: 10000) | `--max-stations` | 0 |
| **OCPPCTL_NO_AUTH** | bool | **`server start`**: accept every station without authentication | `--no-auth` | false |
| **OCPPCTL_OCPP_VERSION** | list | **`client`**: versions to offer; **`server start`**: versions to accept; **`actions`**, **`validate`**, **`frame`**: the one version | `--ocpp-version` | 2.1,2.0.1,1.6 (`client send`: 2.1; offline: 2.0.1) |
| **OCPPCTL_OUTPUT** | string | **`server generate-config`**: file to write (`-`: stdout) | `--output` | - |
| **OCPPCTL_OUTPUT_FORMAT** | string | Output format: text, json or table | `--format` | text |
| **OCPPCTL_PASSWORD** | string | **`client`**: the HTTP Basic password itself, when `--password-file` is not given. Read when the command runs, never shown in `--help` or output | | |
| **OCPPCTL_PASSWORD_FILE** | string | **`client`**: file holding the HTTP Basic password | `--password-file` | |
| **OCPPCTL_PASSWORDS_FILE** | string | **`server start`**: file of `identity:password` lines | `--passwords-file` | |
| **OCPPCTL_PATH** | string | **`server start`**: URL path prefix | `--path` | / |
| **OCPPCTL_PING_INTERVAL** | duration | **`client run`**, **`server start`**: WebSocket ping interval (0: no pings) | `--ping-interval` | 0 |
| **OCPPCTL_RATE_BURST** | int | **`server start`**: burst of `--rate-limit` (0: the rate, rounded up) | `--rate-burst` | 0 |
| **OCPPCTL_RATE_LIMIT** | float | **`server start`**: messages a second per station (0: no limit) | `--rate-limit` | 0 |
| **OCPPCTL_STATION** | string | **`server call`**: identity of the station | `--station` | |
| **OCPPCTL_TIMEOUT** | duration | **`client`**: response timeout of a CALL; **`server call`**: how long to wait for the station | `--timeout` | 30s |
| **OCPPCTL_TLS_CA** | string | **`client`**: PEM file of the CAs that verify the CSMS | `--tls-ca` | system roots |
| **OCPPCTL_TLS_CERT** | string | **`client`**: client certificate (profile 3); **`server start`**: server certificate | `--tls-cert` | |
| **OCPPCTL_TLS_CLIENT_CA** | string | **`server start`**: CAs of station client certificates (profile 3) | `--tls-client-ca` | |
| **OCPPCTL_TLS_KEY** | string | Private key of `--tls-cert` | `--tls-key` | |
| **OCPPCTL_TLS_SERVER_NAME** | string | **`client`**: name to verify the CSMS certificate against | `--tls-server-name` | the URL host |
| **OCPPCTL_URL** | string | **`client`**: CSMS endpoint and station identity | `--url` | |

Durations are Go durations (`30s`, `10m`). Flags without a variable: `--debug`, `--header`, `--payload`, `--raw`, `--payload-only` and the flags of the shortcuts, `--each-version`, `--no-reconnect`, `--stagger`, `--keep-going`, `--var`, `--yes`, `--from`, `--response`, `--no-validate`, the flags of `frame encode` and `cert hash`, and `--output`, `--format`, `--force` of `client generate-config` (`--format`, `--force` of `server generate-config`).

</details>

---

## Known issues and open questions

Everything known to be wrong, missing or undecided in this release is in
**[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)**:

- **Bugs in go-ocpp** (G1 to G3), fixed in go-ocpp v0.1.2 and v0.1.3, which ocppctl
  requires: frames lost on close, integral numbers such as `20000.0` refused for integer
  fields, and a SEND dropped when the peer closes right after it.
- **Findings in the reference implementations** (U1 to U5) from the
  [interoperability run](reports/interop-v0.4.1.tsv), with their root causes.
- **Limitations** of ocppctl (L1 to L9), such as the missing `example` command and the
  fixed set of simulated behaviours.
- **Open questions** (Q1 to Q7) on behaviour that could reasonably go the other way, such
  as booting again after a reconnection. Feedback on these is welcome in an issue.

---

## FAQ

### What is ocppctl?

ocppctl is a command-line tool for OCPP 1.6J, 2.0.1 and 2.1, the protocol between EV charging stations and their CSMS. As a Charging Station it probes a CSMS, sends any message, simulates one or many stations and runs test scenarios; as a CSMS it accepts stations, answers them from a model and sends them CALLs. It validates every message against the OCA schemas.

### How do I check which OCPP version my CSMS supports?

`ocppctl client probe --url wss://csms.example.com/ocpp/CS001 --each-version` connects once per version and lists the versions the CSMS accepts. Without `--each-version` it reports the version the CSMS picks from all three. See [Is my CSMS reachable](#is-my-csms-reachable-and-which-version-does-it-pick).

### The CSMS refuses the connection. What should I check?

- **HTTP 401**: the password. It comes from `--password-file` or `OCPPCTL_PASSWORD`, and the user name is the identity in the URL.
- **HTTP 404**: the identity (the CSMS does not know it) or the path before it.
- **`no_common_version`**: the CSMS speaks none of the `--ocpp-version`s offered; `--each-version` shows which it does.
- **`transport_tls_failed`**: the CSMS certificate is not trusted: give its CA with `--tls-ca`. For profile 3, check `--tls-cert` and `--tls-key`.
- **Everything else**: `--debug` shows go-ocpp's log and every frame.

### How do I send a message that is not one of the shortcuts?

`ocppctl client call <Action> --url … --payload '{…}'` (or `@file`, or `-` for stdin). `ocppctl actions --ocpp-version 2.0.1 --from station` lists the actions a station sends; `--payload-only` of a shortcut gives a payload to start from.

### Why does ocppctl refuse my payload?

It does not conform to the OCA schema of the version the CSMS chose; the error names the field and the reason. `ocppctl validate <Action> --ocpp-version <v> --payload …` checks it offline. To send it anyway, add `--raw`.

### How do I simulate many charging stations?

`ocppctl client run --url 'wss://csms/ocpp/LOAD{001..100}' --stagger 50ms --duration 15m --format json > events.jsonl` runs LOAD001 to LOAD100 from one model; a summary of calls, latency and errors goes to stderr. See [docs/station-model.md](docs/station-model.md#many-stations).

### How do I test a real charging station without a CSMS?

`ocppctl server start --insecure-transport --no-auth` (or with TLS and passwords), point the station at `ws://<host>:9000/<identity>`, watch the events, and send it CALLs with `ocppctl server call … --yes` from another terminal. See [docs/server-config.md](docs/server-config.md).

### How do I use ocppctl in scripts and CI?

Pass `--format json` (or set `OCPPCTL_OUTPUT_FORMAT=json`). Progress goes to stderr, the result to stdout, and a failure returns one JSON error object with a stable exit code. `client script` exits 7 when a step fails. See [docs/json-output.md](docs/json-output.md), [docs/exitcodes.md](docs/exitcodes.md) and [docs/scenarios.md](docs/scenarios.md).

### Can I turn off TLS certificate verification?

No. go-ocpp does not allow it, and neither does ocppctl. Trust a test CA with `--tls-ca`, and use `--tls-server-name` when the certificate names another host.

### Which implementations has it been tested with?

ocppctl v0.1.0 was run against the five reference implementations of [ocpp-interop](https://github.com/otfabric/ocpp-interop) v0.4.1 with `scripts/interop.sh`, with every fixture sample in both directions: ocppctl as the station against the CSMSs of mobilityhouse (1.6, 2.0.1, 2.1), ChargeTime (1.6, 2.0.1), lorenzodonini (1.6, 2.0.1) and CitrineOS (1.6, 2.0.1, 2.1), and the stations of mobilityhouse, ChargeTime, lorenzodonini and EVerest libocpp (1.6, 2.0.1, 2.1) against ocppctl's server. Of 1218 exchanges, 1154 passed, 29 were answered with a CALLERROR, 32 were skipped because the reference could not send the message, and 3 failed on schema-invalid responses of ocpp-go; every one is explained in [docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md). If you use ocppctl with a CSMS or station it has trouble with, please [open an issue](https://github.com/otfabric/ocppctl/issues) with the `--debug` trace.

### Is it safe to point ocppctl at a production CSMS?

Read-only commands (`probe`, `heartbeat`, `authorize`) are harmless to most CSMSs, but a CSMS records every connection and BootNotification, and it bills transactions. Use identities and tokens meant for testing, and know that a connection under the identity of a real station may disconnect that station. See [SECURITY.md](SECURITY.md).

---

## Project structure

<details>
<summary>Repository layout</summary>

```console
ocppctl/
├── cmd/ (root command, offline commands, completion, version)
│   ├── client (probe, call, send, shortcuts, transaction, run, script, generate-config)
│   └── server (start, call, stations, generate-config)
├── internal/
│   ├── config (flag and environment binding)
│   ├── cli (shared Cobra helpers: enum completion, required flags, debug)
│   ├── conn (the connection to a CSMS: URL, versions, credentials, TLS, frame trace)
│   ├── clientops (the one-shot client commands: probe, call, send, shortcuts)
│   ├── payload (the payloads of the shortcuts, per version)
│   ├── answer (default answers to CALLs that arrive meanwhile)
│   ├── station (the simulated station: model, validation, behaviour)
│   ├── script (scenarios: format, matchers, variables, runner)
│   ├── csms (the CSMS: model, answers, control socket)
│   ├── offline (actions, validate, frame, cert hash)
│   ├── ocppx (versions, actions and schema validation over go-ocpp)
│   ├── ocpperr (go-ocpp errors as typed errors with exit codes)
│   ├── testcsms (an in-process CSMS for tests, over go-ocpp's ocpptest)
│   ├── format (stdout text/json/table, streaming output)
│   ├── runner (command wiring, formatted and streaming runs, fatal rendering)
│   ├── errs (typed errors, stable codes, exit-code mapping)
│   └── types (the results and events written to stdout)
├── examples/ (scenarios and payloads)
├── scripts/interop.sh (interoperability run against ocpp-interop)
├── docs/ (exit codes, JSON output, station model, scenarios, CSMS, design, known issues)
├── Makefile (build and test commands)
└── go.mod (dependencies)
```

</details>

## Development

A toplevel `Makefile` is provided for convenience. Run **`make`** (or **`make help`**) for the full list. Common targets:

| Target | Purpose |
|--------|---------|
| **`make test`** | Unit and end-to-end tests with race detector (`go test -race ./...`) |
| **`make coverage`** | Tests + **`coverage.out`** across the whole module; fails below **85%** total statement coverage (`COVERAGE_MIN`) |
| **`make compile`** | Full-module typecheck (`go build ./...`) |
| **`make lint`** | `compile` then **staticcheck** |
| **`make lint-ci`** | `compile`, **go vet**, **golangci-lint** (uses `.golangci.yml`) |
| **`make fmt`** / **`fmt-check`** | Format with the gofmt of the Go version in `go.mod` (the one CI uses); `fmt-check` fails if that gofmt or the locally installed one would change any file |
| **`make check`** | `fmt`, `fmt-check`, `lint`, `lint-ci`, `vet`, `test`, **`coverage`** |
| **`make build`** | Runs **`check`** then writes **`bin/ocppctl`** |
| **`make build-all`** / **`release-all`** | Cross-compile and package archives |

### Building

`make build` writes the binary to **`bin/ocppctl`** (not the repo root).

```console
make build
make build-all
```

### Running Tests

```console
make test
make coverage
```

Tests need no charger and no network beyond the loopback interface: they run the real command tree in process against go-ocpp's `ocpptest` stations and CSMS (`internal/testcsms`) and against ocppctl's own server, in every version, over ws:// and TLS.

### Interoperability

`scripts/interop.sh [tag]` runs ocppctl against the reference implementations of [ocpp-interop](https://github.com/otfabric/ocpp-interop) (default tag v0.4.1) with every fixture sample, in both roles, and writes `reports/interop-<tag>.tsv`. It needs Docker and jq.

## Version History

Release notes for each version are maintained in **[RELEASE.md](RELEASE.md)**. The original design and the decisions taken are in [docs/DESIGN.md](docs/DESIGN.md).

---

## Contributing

Bug reports, interoperability notes and pull requests are welcome. Please [open an issue](https://github.com/otfabric/ocppctl/issues) to discuss larger changes first, and run `make check` before submitting. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and guidelines.

Please report security vulnerabilities privately, as described in [SECURITY.md](SECURITY.md), not in a public issue.

If ocppctl is useful to you, a star on GitHub helps other engineers find it.

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
