---
type: Security Boundary
title: Remote artifacts gates
description: The conditions a paired device must meet before the artifacts socket serves it anything.
resource: internal/server/artifacts_consent.go
tags: [security, artifacts, remote, e2e]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/artifacts_consent.go]
---
Everything is off by default and only the desk can switch it on: every artifacts command is refused when it arrives through the relay (`refusedArtifactsThroughRelay`).

Three conditions must all hold (`artifactCheck`, run when a socket opens and on every request, from prefs read off disk, failing closed if unreadable):

1. A kind is offered, switched on, and its acknowledgement matches `artifactAckVersion` (raise it to ask everyone again). Only `recordings` is offered.
2. The device is on the allowlist (`RemoteArtifactsPrefs.HasDevice`).
3. The device's end-to-end key for that origin was verified by the person at the desk (`ArtifactVerifier.DeviceVerified`, backed by [remote_verified.json](/data/remote-verified-file.md)). With no verifier set, nobody is verified.

Further controls: mandatory e2e encryption (no plaintext fallback), the key after the handshake must equal the key that was checked, the client never names a path (ids are issued per socket), file access goes through a confinement root (see [artifact root confinement](/security/artifact-root-confinement.md)), recordings are redacted and clipped again before sending, per-device rate and byte limits, an [audit log](/data/artifacts-audit-log.md) whose write failure refuses the request, and kill switches: `stopRemoteArtifacts`, `revokeArtifactDevice`, and `POST /remote/artifacts/stop`.

The control socket never carries the artifact device list to relay windows (`prefsFor` blanks `RemoteArtifacts`), because the relay can read that socket. Details of the protocol: [/ws/artifacts](/api/ws-artifacts.md).
