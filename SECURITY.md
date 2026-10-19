# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `ocppctl`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/ocppctl/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue
- The affected version(s) (`ocppctl version` output)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### OCPP Controls Real Chargers

A CSMS starts and stops charging sessions, resets stations, changes their configuration and installs their firmware and certificates; a station reports the energy a CSMS bills. `ocppctl` speaks both sides of that conversation.

- **Use it only on stations, CSMSs and networks you own or are authorized to test**
- Use identities and id tokens meant for testing. A CSMS may bill the sessions of a simulated station, and a connection under the identity of a real station replaces or is refused next to the real one
- Keep station traffic encrypted (security profiles 2 and 3) wherever it leaves a closed test network

### Security Profiles

| Profile | Transport | Authentication of the station |
|---|---|---|
| 0 | ws:// | none |
| 1 | ws:// | HTTP Basic password |
| 2 | wss:// (TLS) | HTTP Basic password |
| 3 | wss:// (TLS) | client certificate |

- **`ws://` needs `--insecure-transport`**, for the client and the server. Without TLS everything is sent in clear text, the Basic password included, and anyone on the path can read and change the messages. Profiles 0 and 1 belong on closed test networks only
- TLS 1.2 is the minimum. **TLS verification cannot be turned off**: go-ocpp does not allow it, and neither does ocppctl. To trust a private or test CA, give it with `--tls-ca`; when the certificate names another host, use `--tls-server-name`
- With profile 3 the server takes the identity from the client certificate's CommonName; it must match the identity in the URL

### Passwords

- The client reads the HTTP Basic password from **`--password-file`** or the **`OCPPCTL_PASSWORD`** environment variable, never from a flag value or the URL (a URL with credentials is refused). The variable is read when the command runs, so `--help` never shows it
- The server reads station passwords from its model (`stations[].password`) or **`--passwords-file`** (`identity:password` lines). A malformed line is reported by its number, never by its content
- Passwords are never printed, logged or traced. Keep the files readable by you alone (`chmod 600`); a model with passwords in it is such a file too. `server generate-config` creates its output file with mode 0600
- In CI, pass `OCPPCTL_PASSWORD` from the secret store of the pipeline

### Private Keys

`--tls-key` files (the station's client key, the server's key) are read from the files you name and are never written or printed. Keep them readable by you alone.

### Commands That Change a Station or a CSMS

- **`server call`** sends a CALL to a connected station: Reset, RequestStartTransaction / RemoteStartTransaction, RequestStopTransaction, ChangeAvailability, UnlockConnector, SetChargingProfile, ChangeConfiguration, SetVariables, UpdateFirmware, InstallCertificate, DeleteCertificate and the like act on the station as soon as it accepts them. **Every CALL that may change the station needs `--yes`** (exit 2 without it); only the read-only Get… requests are sent without. GetLog and GetDiagnostics make a station upload files and need `--yes` too. A CALL is sent once: a timeout of a state-changing CALL is `outcome_unknown`, and it is not sent again
- **`client call`**, the shortcuts and **`client script`** send CALLs to a CSMS: StartTransaction and TransactionEvent create sessions the CSMS may bill, MeterValues and StopTransaction report energy, a BootNotification registers the station, StatusNotification changes what the CSMS shows to drivers. A state-changing CALL is sent once and never retried
- **`client run`** starts transactions by itself (the `sessions` of the model) and when the CSMS asks it to. Against a production CSMS, that is real charging data
- `--raw` sends payloads that do not conform to the schema, on purpose. Use it against systems you are testing, not against production

### Running a Server

- `server start` listens on **all network interfaces** by default (`--listen :9000`). Use `--listen 127.0.0.1:9000` to bind to one address, `--allow` to restrict the clients, or a host firewall
- **Authentication is required** unless `--no-auth` is given: passwords, client certificates, or both. `--no-auth` accepts any station under any identity
- The **control socket** lets `server call` and `server stations` reach the server. It is created with mode 0600, by default as `ocppctl.sock` in the **working directory**, and removed on exit; `--control-socket ''` disables it. Anyone who can open it can send CALLs to the connected stations without `--yes`, which is a check of the `server call` command, not of the socket. Do not place it in a directory others can write to
- The server answers what its model says: a model that accepts every token (the default) lets every station charge

### Output

- Payloads, events, scenario results and `--debug` traces contain station identities, id tokens (RFID UIDs, eMAIDs), transaction ids, meter values and network addresses, which may be personal or commercially sensitive data. Be careful when storing or sharing them, and do not attach them to public issues unredacted
- `--debug` prints every OCPP frame sent and received, and go-ocpp's log, to stderr. It does not trace the WebSocket handshake headers, so passwords do not appear in it

### Privileges

`ocppctl` needs no elevated privileges. `server start` on port 9000 works as a normal user; binding a port below 1024 (443, for one) may need a capability (for example `CAP_NET_BIND_SERVICE` on Linux) rather than root.

### Verifying Downloads

Release archives are published with SHA-256 checksums. Verify them as shown in the [installation instructions](README.md#installation) before installing.
