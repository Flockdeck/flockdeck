---
type: CLI Command
title: flockdeck peer-name
description: Tells the instance the name another Claude session would use to address this pane.
resource: main.go
tags: [cli, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
## Synopsis

`flockdeck peer-name <name>`

Takes exactly one non-blank argument and no flags beyond `-h`. The name is what a `SendMessage` "to" field would use, as the agent's own `ListAgents` tool shows it. Flockdeck cannot learn it on its own, so the agent reports it. The instance shows it in the pane header.

## Where it runs

Inside a pane. Needs [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md); [FLOCKDECK_PANE](/config/flockdeck-pane.md) identifies the pane.

## Behaviour

Calls `hooks.PeerName` on the loopback API and prints `reported peer name <name>`. Without the pane variables it fails with the same "this only works inside a flockdeck pane" message as [spawn](/cli/spawn.md). A missing or blank name prints the usage and exits 1.
