---
type: WebSocket Message
title: Control commands: remote access and artifacts
description: Commands on /ws/control for pairing devices, enabling the relay tunnel, device roles and remote artifacts.
resource: internal/server/remote.go
tags: [websocket, control, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/remote.go, internal/server/viewerrole.go, internal/server/control.go, internal/server/artifacts_consent.go, internal/server/remote_verify.go]
---
Part of [/ws/control](/api/ws-control.md). Files: `remote.go`, `remote_verify.go`, `viewerrole.go`, `artifacts_consent.go`. The mutating ones are refused when the sender is a relay window (artifacts commands via `refusedArtifactsThroughRelay`); all are `accessFull`.

| `cmd` | Fields | Effect and reply |
|---|---|---|
| `remoteDevices` | | `remoteDevices`: enabled, devices, hosts, current, error, plan |
| `remotePair` | `kind` (`device` or `host`; `host` refused from a relay window) | `remotePair`: kind, code, url, expiresAt, qr (SVG), error |
| `remoteRevoke` | `id` | Revoke a device at the relay |
| `remoteRename` | `kind` (`host`), `id`, `name` | Rename this machine or a device |
| `remoteVerify`, `remoteUnverify` | `id`, `kind` (key origin), `text` (typed code) | Record or clear the fingerprint check; refused from relay windows |
| `remoteEnable` | `relay`, `name`, `join`, `invite` | Enrol this machine; replies `remoteOutcome` |
| `remoteDisable`, `remoteRemove` (`force`), `remoteMove` (relay as above), `remoteReconnect` | | `remoteOutcome`: action, error, untold, warning |
| `setDeviceRole` | `id` (device), `kind` (`full` or `viewer`), `watch` (viewer may watch panes) | Desk only. Changes a device's role and ends that device's sockets |
| `setRemoteArtifacts` | | Switch artifact kinds, acknowledgements and device allowlist |
| `revokeArtifactDevice` | `id` | Remove from allowlist and close its sockets |
| `stopRemoteArtifacts` | | Close every artifacts socket now |

The `state` snapshot carries a `remote` object (state, detail, since, retryAt, relay, hostId, name, viewers, pushError) when this machine is enrolled. Relay-side behaviour is described in [remote package](/packages/remote.md); roles in [device access roles](/security/device-access-roles.md).
