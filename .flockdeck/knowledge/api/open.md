---
type: HTTP Endpoint
title: POST /open
description: Hands a directory to the running instance so a second launch attaches instead of starting a rival server.
resource: internal/server/attach.go
tags: [http, attach, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go]
---
Handler: `Server.handleOpen`. `POST` only (`405` with `Allow: POST` otherwise). URL token required.

Query: `path` (required, else `400 no path`).

Behaviour: queues `ws.OpenProject(path)` on the workspace goroutine; one 10 second budget (`openTimeout`) covers handing over and hearing the result.

| Status | When |
|---|---|
| 204 | project opened |
| 202 | taken but still running when the budget ended |
| 400 | `OpenProject` returned an error (message is the body) |
| 503 | the request never reached the workspace in time ("the instance is busy"), or shutting down |
