---
type: File Format
title: remote.json
description: This machine's enrolment with a relay: relay address, host id, account id, secret token, name, enabled flag.
resource: internal/remote/config.go
tags: [remote, state, secret]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/config.go]
---
Stored as `remote.json` in the state directory (the config dir), written atomically by `store.WriteAtomic` with owner-only permissions.

| Field | Type | Meaning |
|---|---|---|
| `relay` | string | relay base address (https, or http only to loopback) |
| `hostId` | string | this machine's id on the relay |
| `accountId` | string | account id |
| `token` | string | credential the relay knows this host by. A secret: it can mint pairing codes. Never printed, sent only to its relay |
| `name` | string, optional | display name of the machine |
| `enabled` | bool | always written; `false` keeps the enrolment but closes the tunnel |

A file without `enabled` (older versions) reads as enabled (`UnmarshalJSON`).

`Load` returns nil when the file is absent. It errors, rather than reporting "not enabled", on invalid JSON or missing `relay`, `hostId` or `token`, and on a hand-edited `http://` or `ws://` relay that is not loopback (the token would cross the network in the clear). Reads wait out a concurrent save (`store.ReadState`). `Clear()` deletes the file and is called only by `Remove`.

See [remote package](/packages/remote.md).
