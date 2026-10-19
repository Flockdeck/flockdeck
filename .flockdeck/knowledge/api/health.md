---
type: HTTP Endpoint
title: GET /health
description: Liveness and readiness probe a second launch uses to decide whether a recorded instance is alive.
resource: internal/server/attach.go
tags: [http, attach, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go]
---
Handler: `Server.handleHealth`. Any method is accepted. Requires the token in the URL (`?t=`), see [URL token endpoints](/security/url-token-endpoints.md); a remote request cannot satisfy it.

The handler asks the workspace goroutine for the number of open projects within `healthTimeout` (1 second, half of `probeTimeout`).

Response `application/json`, `healthMsg`:

| Field | Meaning |
|---|---|
| `app` | `"flockdeck"` |
| `version` | `server.Version` (set by main, default `dev`) |
| `pid` | process id |
| `projects` | open project count |
| `ready` | true when the workspace answered |
| `windows` | local windows connected (relay windows excluded) |

If the workspace does not answer in time the reply is `503` with the same JSON but `ready:false` and `projects:0`, so the caller waits instead of starting a rival instance. `503 shutting down` when the server is closing.

Client: `server.Probe(baseURL, token)` retries a not-ready answer for `busyGrace` (5 s, every 250 ms) and returns `ErrNotReady` after that. See [health-not-ready gotcha](/gotchas/health-not-ready-is-alive.md).
