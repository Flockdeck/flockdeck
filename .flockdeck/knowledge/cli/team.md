---
type: CLI Command
title: flockdeck team
description: Lists the agent panes of the calling pane's project with role, name, agent and status.
resource: cli_team.go
tags: [cli, pane, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_team.go, internal/hooks/team.go, internal/team/team.go]
---
## Synopsis

`flockdeck team [-json]`

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-json` | false | Print the roster as indented JSON; an empty roster prints `[]`. |

## Where it runs

Inside a pane. `paneCaller` requires all three of [FLOCKDECK_API](/config/flockdeck-api.md), [FLOCKDECK_TOKEN](/config/flockdeck-token.md) and [FLOCKDECK_PANE](/config/flockdeck-pane.md); if any is empty it fails with "this only works inside a flockdeck pane ...".

## Behaviour

Calls `hooks.Team` and prints one line per member from `team.Lines`, with the caller marked "(you)". With no members it prints "no agent panes in this project". Extra arguments print the usage and exit 1. Related: [msg](/cli/msg.md), [inbox](/cli/inbox.md), [task](/cli/task.md).
