---
type: HTTP Endpoint
title: POST /quit
description: Stops the running instance from outside, used by `flockdeck -quit`.
resource: internal/server/attach.go
tags: [http, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go, internal/server/server.go]
---
Handler: `Server.handleQuit`. `POST` only, URL token required, so no relay window can call it.

It writes `204`, flushes the response so the reply is on the wire before the process ends, then runs `requestQuit` (the `OnQuit` hook) on a new goroutine. The control-socket `quit` command is the other route to the same hook and is refused from remote windows.

Client side: `server.RequestQuit` waits up to `quitGrace` (15 s), polling every `quitPoll` (100 ms), for the instance to stop.
