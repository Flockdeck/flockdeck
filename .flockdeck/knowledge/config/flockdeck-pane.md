---
type: Environment Variable
title: FLOCKDECK_PANE
description: The id of the pane a process is running in; identifies the caller to the instance.
resource: cli_team.go
tags: [config, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_team.go, main.go, internal/chat/flags.go, internal/legacyenv/legacyenv.go]
---
Set by the instance in each pane's environment.

- [team](/cli/team.md), [msg](/cli/msg.md), [inbox](/cli/inbox.md) and [task](/cli/task.md) require it (`paneCaller`), along with [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md).
- [spawn](/cli/spawn.md), [peer-name](/cli/peer-name.md) and [close](/cli/close.md) require only API and token, and pass the pane id along when it is set.
- [chat](/cli/chat.md) uses it as the session (conversation) id when `-session` is not given.

Read through `legacyenv.GetWarn`, with fallback to `PERCH_PANE` and a one-time warning.
