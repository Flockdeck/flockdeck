---
type: CLI Command
title: flockdeck close
description: Closes another pane, or every finished pane, from inside a pane.
resource: main.go
tags: [cli, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
## Synopsis

- `flockdeck close [-force] <pane-id>`
- `flockdeck close -finished`

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-force` | false | Close the pane even while it is working. A locked pane is never closed. |
| `-finished` | false | Close every idle agent and cleanly exited pane across all open projects. Leaves locked panes, idle shells and agents with background work. Takes no pane id and cannot be combined with `-force`. |

## Where it runs

Inside a pane. Needs [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md); passes [FLOCKDECK_PANE](/config/flockdeck-pane.md) if set.

## Output and errors

Sends the request through `hooks.Close`. Prints `closed pane <id>`, or for `-finished` a summary such as `closed 3 finished panes and 1 empty tab; 1 left open because locked`, "no finished panes to close", or "no finished panes closed: N locked". Wrong argument counts print the usage and exit 1; `-h` exits 0.
