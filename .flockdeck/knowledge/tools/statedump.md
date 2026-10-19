---
type: Developer Tool
title: statedump
description: Prints what a running instance reports about its panes.
resource: cmd/statedump
tags: [dev-tool, control-socket]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/statedump]
---
Development aid, not shipped.

```
go run ./cmd/statedump "ws://127.0.0.1:PORT/ws/control?t=TOKEN"
```

Takes only the socket URL (a leading `-` prints usage, exit 2). Dials the control socket, reads the first state snapshot with [controlsock](/tools/controlsock.md), and prints one line per pane, sorted by id: short id (8 characters), kind, "agent | model" (or "-" for a shell), columns x rows, status and error text. Read-only; sends no commands.

See also [ctl](/tools/ctl.md) and [treedump](/tools/treedump.md).
