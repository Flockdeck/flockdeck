---
type: CLI Command
title: flockdeck hook
description: Hidden subcommand a Claude Code pane runs on each lifecycle event to report back to the instance.
resource: main.go
tags: [cli, hidden, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
## Synopsis

`flockdeck hook -endpoint <url> -session <pane-session-id> -event <name> [-token <secret>]`

Not in the usage. Claude Code runs it as a command hook; the event JSON arrives on stdin.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-endpoint` | none, required | The instance's hook endpoint. |
| `-token` | [FLOCKDECK_TOKEN](/config/flockdeck-token.md), read quietly | Shared secret. A value given here wins; kept for panes an earlier build started. |
| `-session` | none, required | Pane session id. |
| `-event` | none, required | Lifecycle event name. |

## Behaviour

- Posts stdin to the instance through `hooks.Emit`. It never fails the session: every error is one line on stderr and the exit status is 0.
- If the reply has a context or an allow decision, it prints Claude Code hook JSON on stdout: `hookSpecificOutput` with `hookEventName` and `additionalContext`, plus `permissionDecision: "allow"` and a reason when auto-review allowed a tool call. It never prints "deny".
- A missing `-endpoint`, `-session` or `-event` prints `flockdeck hook: -endpoint, -session and -event are all required` and returns.
- A flag parse failure returns silently.
