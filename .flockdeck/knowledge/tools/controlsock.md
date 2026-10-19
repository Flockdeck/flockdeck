---
type: Developer Tool
title: cmd/internal/controlsock
description: Shared helper for the dev tools that read a running instance's control socket.
resource: cmd/internal/controlsock
tags: [dev-tool, control-socket]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/internal/controlsock]
---
Library, not a program. One function: `ReadState(ctx, conn)` raises the socket read limit to 16 MiB (a busy workspace's snapshot exceeds the 32 KB default), skips messages until one has `"type":"state"` (a window is first greeted with the key table and preferences), and returns that message.

Used by [ctl](/tools/ctl.md), [statedump](/tools/statedump.md) and [treedump](/tools/treedump.md).
