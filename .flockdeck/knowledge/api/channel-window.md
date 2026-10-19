---
type: HTTP Endpoint
title: POST /window (local channel)
description: Returns a one-time window link, rate limited to 10 per minute.
resource: internal/channel/channel.go
tags: [channel, ipc, window]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/channel/channel.go]
---
# POST /window

Served on the [channel](/packages/channel.md) socket or pipe.

- Request body: none expected; over 1 KiB gets 413.
- Response 200 `text/plain` with whatever `Config.Window()` returns, exactly as the HTTP server's own window request does; the channel adds no rules to it. `Cache-Control: no-store`.
- More than `WindowsPerMinute` (10) in a minute: 429 with `Retry-After: 60`.
- The link is a credential for the window, so the channel's access checks are the only gate ([local channel access](/security/local-channel-access.md)).
