---
type: File Format
title: e2e_key.json
description: This machine's long-term P-256 end-to-end identity used to encrypt terminals and artifacts reached through the relay.
resource: internal/remote/e2ekey.go
tags: [remote, e2e, state, secret]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/e2ekey.go]
---
Stored as `e2e_key.json` in the state directory, beside `remote.json`, written with `store.WriteAtomic` (owner-only).

```json
{ "privateKey": "<base64url P-256 scalar>" }
```

Only the private key is stored; the public half is recomputed and encoded by `internal/e2e.EncodePublicKey`. `ensureE2EIdentity` loads it or generates one with `e2e.GenerateStaticKey` on first use. The public half is registered with the relay (`POST /api/v1/host/e2e-key`, `Manager.EnsureE2EKey`, once the tunnel is up) and sent to relay windows in `hello.e2ePublicKey`.

A missing or unregistered key is not an error for the terminal socket: `E2ECapable` answers false and the terminal is served unencrypted ([relay trust](/security/relay-trust-and-e2e.md)). The artifacts socket refuses instead.

A file that is invalid JSON or holds an invalid key is an error naming the file.
