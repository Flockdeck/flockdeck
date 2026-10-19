---
type: Environment Variable
title: FLOCKDECK_TOKEN
description: Shared secret a pane uses to authenticate to its instance.
resource: main.go
tags: [config, pane, secret]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go, statusline.go, cli_team.go, internal/legacyenv/legacyenv.go]
---
Set by the instance in each pane's environment. The pane subcommands send it to the instance with every call: [spawn](/cli/spawn.md), [peer-name](/cli/peer-name.md), [close](/cli/close.md), [team](/cli/team.md), [msg](/cli/msg.md), [inbox](/cli/inbox.md), [task](/cli/task.md) and [chat](/cli/chat.md).

[hook](/cli/hook.md) and [statusline](/cli/statusline.md) read it too, when `-token` is not given, and read it quietly (no deprecation warning) because they run as a new process on every event. A `-token` value wins. Passing the secret as a flag is kept only for panes an earlier build started, since a command line is readable by other users of the machine.

Read through `legacyenv`, with fallback to `PERCH_TOKEN`. Do not log or commit its value.
