---
type: Package
title: e2e
description: End-to-end encryption of one terminal socket between a paired browser and this desktop.
resource: internal/e2e
tags: [security, crypto, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/e2e/e2e.go]
---
A hand-kept copy of the relay's own e2e package (Go's internal rule stops importing it across modules). Wire format and constants must be changed in both places; `e2e_test.go` catches drift. Used by `internal/remote` and `internal/server`.

| Name | Role |
|---|---|
| `GenerateStaticKey` | P-256 long-term key pair (`crypto/ecdh`). |
| `EncodePublicKey` / `DecodePublicKey` | base64url public key. |
| `Fingerprint(devicePub, hostPub)` | Six 5-digit groups from SHA-256 of both static keys, for comparing on both screens. |
| `StartDeviceHandshake`, `DeviceHandshake.Finish`, `RespondHostHandshake` | Handshake with fresh ephemeral keys per socket. |
| `Session.Seal` / `Session.Open` | Frame: version byte (1), 8-byte big-endian counter, AES-256-GCM body. AAD is version + sender role. Counter must be exactly the next expected. |

See [terminal end-to-end encryption](/security/terminal-e2e-encryption.md).
