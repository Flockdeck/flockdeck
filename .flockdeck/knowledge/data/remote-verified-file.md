---
type: File Format
title: remote_verified.json
description: Records of paired devices whose end-to-end key fingerprint the person at this machine compared.
resource: internal/remote/verified.go
tags: [remote, e2e, state]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/verified.go]
---
Stored as `remote_verified.json` in the state directory, owner-only. At most 1000 records (`maxVerifiedRecords`).

```json
{ "v": 1, "records": [ { "device": "...", "origin": "...", "deviceKey": "...", "hostKey": "...", "at": "RFC3339" } ] }
```

A record binds the device id, key origin (`usual` or `desk`, see `KeyOrigin`), the device's public key and this machine's public key. If any changes the device counts as not verified (`KeyChanged` state, never edited into agreement). Unreadable records mean none are verified (fail closed).

Two Flockdeck processes can share the directory, so the file is re-read when its mtime or size changes, before every check and change. Written only by `Manager.VerifyDevice`, which the server calls only for windows on this machine; `ForgetDevice`/`ForgetDevicesNotIn` prune records.

`DeviceVerified` is what the artifacts socket requires ([gates](/security/artifacts-socket-gates.md)). The file does not defend against software already running as the same user.
