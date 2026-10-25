---
type: CLI Command
title: flockdeck pane-name
description: Names a pane for the work it does, so the window and other agents call it that and not by its directory name.
resource: main.go
tags: [cli, pane]
timestamp: 2026-10-25T00:00:00Z
code_paths: [main.go, internal/hooks/team.go, internal/workspace/panelabel.go]
---
## Synopsis

`flockdeck pane-name [-pane <id>] <label>`

The label is the rest of the arguments joined by spaces: one line of at most 40 characters, in 2-5 words (the PR or topic). An empty label clears it. A label that could pass for the user or for Flockdeck is shown as "(agent) ...".

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-pane <id>` | this pane | The pane to name. A pane may name itself; a manager may name any pane of its own project; any other pane is refused. |

## Where it runs

Inside a pane. Needs [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md); [FLOCKDECK_PANE](/config/flockdeck-pane.md) identifies the pane. Without them it fails with the same "this only works inside a flockdeck pane" message as [spawn](/cli/spawn.md).

## Behaviour

Calls `hooks.PaneName` on the loopback API and prints `pane <id> is now called "<label>"`, or `cleared the name of pane <id>`. The label is kept with the pane and survives a restart, unlike the [peer name](/cli/peer-name.md). A label taken from a task's first words is not sent to a relay; one chosen with this command or `spawn -name` is.
