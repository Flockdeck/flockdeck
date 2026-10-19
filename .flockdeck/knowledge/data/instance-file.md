---
type: File Format
title: Running instance record (instance.json)
description: Record of the running Flockdeck process so later launches attach to it.
resource: internal/store/store.go
tags: [store, persistence, instance]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go, internal/store/startlock.go]
---
`instance.json` in the config dir. Fields: `pid`, `url` (local server address), `token` (local server auth token; the value is never documented), `started`.

- `LoadInstance` returns nil if the file is missing, unparseable, or has an empty `url`. A stale record is not detected there; the caller probes the address.
- `ClearInstance` leaves the record alone when it names another process that is still alive, so an exiting instance cannot delete a rival's record.
- `Instance.StillRunning` and `ProcessAlive` test the pid (platform files `process_*.go`).
- `start.lock` in the same directory is an OS file lock (`TryLockStart`, errors with `ErrStartLocked`) held from looking for an instance until this one has recorded itself; the system releases it if the process dies.

The file is written 0600 because it holds the token. See [state directory permissions](/security/state-dir-permissions.md).
