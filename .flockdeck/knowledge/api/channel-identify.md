---
type: HTTP Endpoint
title: POST /identify (local channel)
description: Tells a command-line client which Flockdeck instance owns a socket or pipe.
resource: internal/channel/channel.go
tags: [channel, ipc, identify]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/channel/channel.go]
---
# POST /identify

Served on the [channel](/packages/channel.md) socket or pipe, not on a TCP port. The host in a URL is ignored (the client uses `http://channel/identify`).

- Request body: none expected; one over 1 KiB gets 413.
- Response 200 JSON `{"app": "flockdeck", "pid", "version", "started"}` with `Cache-Control: no-store`. `version` is omitted when unset; `started` is a timestamp.
- Subject to the connection checks in [local channel access](/security/local-channel-access.md).
