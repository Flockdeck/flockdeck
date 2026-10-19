---
type: HTTP Endpoint
title: POST /remote/artifacts/stop
description: Closes every artifacts socket and reloads preferences after `flockdeck remote artifacts off`.
resource: internal/server/artifacts_consent.go
tags: [http, remote, artifacts, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/artifacts_consent.go, internal/server/attach.go]
---
Handler: `Server.handleArtifactsStop`.

- A request through the relay is refused with `403` before anything else. Unlike the other action endpoints it does not call `authorised` first; it relies on `requirePost` and `requireURLToken`.
- `POST` and URL token required.
- Closes all artifacts sockets, then on the workspace goroutine rereads prefs from disk (`store.ReadPrefs`), keeps the in-memory device roles (`AdoptAccess`), republishes the access table and broadcasts prefs.
- Replies once the running copy has been updated, so the CLI and tests can rely on it.

See [artifacts socket](/api/ws-artifacts.md).
