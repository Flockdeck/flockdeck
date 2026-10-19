---
type: CLI Command
title: flockdeck statusline
description: Hidden bridge that reports a Claude pane's cost and rate limits to the instance and runs the user's own status line.
resource: statusline.go
tags: [cli, hidden, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [statusline.go, internal/hooks/usage.go, internal/spend/spend.go, internal/session/claudeshell.go]
---
## Synopsis

`flockdeck statusline -endpoint <url> -session <id> [-token <secret>] [-then <base64url>]`

Claude Code runs it as the pane's status line when that line is routed through Flockdeck. Claude's status JSON arrives on stdin (read up to 16 MiB).

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-endpoint` | none | Where to post the figures. If this or `-session` is empty, nothing is posted. |
| `-token` | [FLOCKDECK_TOKEN](/config/flockdeck-token.md) | Shared secret, as in [hook](/cli/hook.md). |
| `-session` | none | Pane session id. |
| `-then` | none | The user's own status line command, encoded as raw base64url. |

## Behaviour

- Posts a `spend.Report` through `hooks.Report` with a 1 second timeout: model, token totals, `total_cost_usd` as an agent-sourced cost, and each `rate_limits` window with its used percentage and reset time. Windows are dropped when the Claude config folder cannot be found.
- Runs the `-then` command through `session.ClaudeShellCommand`, with the same stdin, in parallel with the post, and passes its stdout and stderr through. A `-then` value that does not decode is not run; a message goes to stderr.
- Exit code is the user's command's exit code, or 0 when there is none. A command that cannot start gives 1. A flag parse failure gives 2. Post failures only write to stderr.
