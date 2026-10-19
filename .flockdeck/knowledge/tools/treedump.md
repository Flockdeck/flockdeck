---
type: Developer Tool
title: treedump
description: Prints the tab and split structure a running instance is showing.
resource: cmd/treedump
tags: [dev-tool, control-socket]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/treedump/main.go, cmd/internal/controlsock/controlsock.go]
---
Development aid, not shipped.

```
go run ./cmd/treedump [-full] "ws://127.0.0.1:PORT/ws/control?t=TOKEN"
```

- Reads the first state snapshot via [controlsock](/tools/controlsock.md) and prints each tab's layout tree (splits and panes).
- Pane ids are shortened to 8 characters; `-full` prints the full ids that [ctl](/tools/ctl.md) accepts, so a rearrangement sent with ctl can be read back without looking at the window.
