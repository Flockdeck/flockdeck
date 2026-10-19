---
type: Developer Tool
title: ctl
description: Sends JSON commands to a running instance over its control WebSocket.
resource: cmd/ctl
tags: [dev-tool, control-socket]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/ctl/main.go, cmd/internal/controlsock/controlsock.go]
---
Development aid, not shipped.

```
go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"cmd":"splitPane","dir":"h"}' ...
```

- No flags. The first argument must be the socket URL; an argument starting with `-` prints usage and exits 2.
- Reads the first `state` snapshot (via [controlsock](/tools/controlsock.md)), finds the focused pane of the active tab, and prints it.
- Each later argument is one JSON command named by `cmd`. If it has no `id`, the focused pane's id is added.
- After each write it waits for the next `state` snapshot before sending the next command. `notice` messages print as `notice:` and error notices as `refused:`.
- The URL carries the per-run token, which is in the [instance file](/data/instance-file.md). Does not run on its own from any Makefile or workflow.

Companion tools: [statedump](/tools/statedump.md), [treedump](/tools/treedump.md).
