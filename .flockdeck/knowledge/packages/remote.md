---
type: Package
title: remote
description: Client side of Flockdeck Remote: enrolment, the outbound tunnel to the relay, device roster, end-to-end keys and push sealing.
resource: internal/remote/manager.go
tags: [remote, relay, e2e, tunnel]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/manager.go, internal/remote/client.go, internal/remote/qr.go, internal/remote/e2ekey.go, internal/remote/connector.go, internal/remote/config.go, internal/remote/artifactkey.go, internal/remote/enrol.go, internal/remote/verified.go, internal/remote/verify.go, internal/remote/webpush.go, main.go, cli_remote.go]
---
The desktop never listens for the network. It dials out to a relay, holds one WebSocket open, runs a `smux` multiplexer over it, and the relay opens a stream per browser connection. Each stream is served by the normal [server](/packages/server.md) handlers with the request marked remote (`Server.ServeRemote`). The relay itself is a separate private project and is not described here.

## Files and pieces

| File | Contents |
|---|---|
| `config.go` | `Config` (see [remote.json](/data/remote-config-file.md)), `RelayURL`, `CheckRelay`, `DefaultRelay`, `RelayEnv` ([config](/config/flockdeck-relay.md)) |
| `client.go` | `Client`: relay REST calls with `Authorization: Bearer <host token>` and `Flockdeck-Version`; `Register`, `Probe`, `Pair`, `Devices`, `Revoke`, `RenameHost`, `RenameDevice`, `Unregister`, `AccountPreview`, `DeleteAccount`, `SetE2EKey`; types `Device`, `Host`, `Roster`, `Plan`, `APIError` |
| `connector.go` | `Connector`: dial, tunnel, reconnect with backoff, `Status` and `State` |
| `manager.go` | `Manager`: owns the connector; `Reload`, `Enable`, `Move`, `Disable`, `Remove`, `Rename`, `Reconnect`, `Status`, `Client`, `Close`. Implements `server.RemoteAccess` |
| `enrol.go` | `Enable`, `Move`, `Disable`, `Remove`, `Rename`; name cleaning (max 64) |
| `e2ekey.go`, `artifactkey.go` | host e2e identity file, roster lookup of device keys (10 s TTL), `E2ECapable`, `E2ERespond`, `E2EDeviceKey` |
| `verified.go` | records of devices whose fingerprint was compared, see [remote_verified.json](/data/remote-verified-file.md) |
| `verify.go` | email verification flow for registering a new machine |
| `webpush.go` | seals Web Push payloads per device (RFC 8291) and posts them to the relay |
| `qr.go` | `QRSVG`, `QRTerminal` for pairing links |

## Relay calls made by this repo

| Method and path | Purpose |
|---|---|
| `GET /healthz` | `Probe` |
| `POST /api/v1/hosts` | register this machine (`name`, `join`, `invite`, `verificationCode`) |
| `POST /api/v1/register/start`, `GET /api/v1/register/status` | email verification |
| `GET /api/v1/host/connect` | tunnel WebSocket, subprotocol `flockdeck-relay.v1` |
| `POST /api/v1/host/pairings` | pairing code (`kind` device or host) |
| `GET /api/v1/host/devices`, `DELETE`/`PATCH /api/v1/host/devices/<id>` | roster, revoke, rename |
| `PATCH`/`DELETE /api/v1/host` | rename or unregister this machine |
| `GET`/`DELETE /api/v1/host/account` | preview or delete account |
| `POST /api/v1/host/e2e-key` | register this machine's public key |
| `GET /api/v1/host/push/devices`, `POST /api/v1/host/notify` | push targets, sealed messages (up to 32 devices) |

## Tunnel behaviour

- Close codes from the relay: 4001 revoked (stop), 4002 replaced by another connection for the same host (stop), 4003 lapsed plan (retry every 10 minutes).
- Backoff 1 s to 1 min; ping every 15 s, silent for 45 s means gone. Dial timeout 20 s.
- `State` values: `off`, `connecting`, `connected`, `disabled`, `revoked`, `replaced`, `error`, `lapsed`.
- Flockdeck decides nothing by plan; it shows the relay's own words.

Security rules are in [relay trust and e2e](/security/relay-trust-and-e2e.md) and [relay address validation](/security/relay-address-validation.md). Dependents: `internal/server`, root `main.go`, `cli_remote.go`.
