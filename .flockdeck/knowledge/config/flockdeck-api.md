---
type: Environment Variable
title: FLOCKDECK_API
description: Loopback address of the instance a pane belongs to; set in each pane's environment.
resource: main.go
tags: [config, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go, cli_team.go, internal/workspace/workspace.go]
---
Set by the instance in the environment of every pane it starts. Subcommands run inside a pane read it to find the instance to call back: [spawn](/cli/spawn.md), [peer-name](/cli/peer-name.md), [close](/cli/close.md), [team](/cli/team.md), [msg](/cli/msg.md), [inbox](/cli/inbox.md) and [task](/cli/task.md).

- Read with `paneEnv("API")` in the root program.
- Unset or empty, these commands fail with "this only works inside a flockdeck pane".
- Used together with [FLOCKDECK_TOKEN](/config/flockdeck-token.md) and [FLOCKDECK_PANE](/config/flockdeck-pane.md).
