---
type: HTTP Endpoint
title: POST /remote/reload
description: Tells a running instance to reread the remote enrolment after `flockdeck remote enable/disable/remove`.
resource: internal/server/remote.go
tags: [http, remote, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/remote.go]
---
Handler: `Server.handleRemoteReload`. `POST` only, URL token required.

Calls `RemoteAccess.Reload()` (`remote.Manager.Reload`, which rereads `remote.json` and starts, restarts or stops the tunnel). `204` on success; `503` "this instance has no Flockdeck Remote to reload" if remote access was never attached; `500` with the error otherwise.

Client: `server.RequestRemoteReload(baseURL, token)` with a 15 second timeout; any status 300 or higher is an error. See [remote package](/packages/remote.md) and [remote.json](/data/remote-config-file.md).
