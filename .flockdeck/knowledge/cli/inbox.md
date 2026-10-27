---
type: CLI Command
title: flockdeck inbox
description: Lists, waits for, streams and reads the messages other agents sent to this pane.
resource: cli_team.go
tags: [cli, pane, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_team.go, internal/hooks/team.go, internal/workspace/team.go]
---
Direction (27 Oct 2026): `flockdeck inbox` is planned for removal with the shell inbox. Status: planned removal. See docs/acp-direction.md.

## Synopsis

- `flockdeck inbox [-all] [-json] [-wait <duration>]`
- `flockdeck inbox -follow`
- `flockdeck inbox read [-json] <id>`

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-all` | false | Include messages already read. |
| `-json` | false | Print messages as JSON. |
| `-wait <duration>` | 0 | With no unread messages, poll every 2 seconds for up to this long (capped at 10 minutes) and print as soon as one arrives. Errors during the wait are retried; the command fails only if no look succeeded. |
| `-follow` | false | Keep running and print one line per new unread message (no text), until the pane closes. Marks nothing read. Takes no other flag. |

`inbox read <id>` takes `-json` and accepts `#12` or `12`. It prints the message in full and marks it read.

## Where it runs

Inside a pane; requires [FLOCKDECK_API](/config/flockdeck-api.md), [FLOCKDECK_TOKEN](/config/flockdeck-token.md) and [FLOCKDECK_PANE](/config/flockdeck-pane.md). Messages come from [msg](/cli/msg.md) through `hooks.Inbox` and `hooks.ReadMessage`.

## Output

The list starts with a warning that sender names and roles are not authenticated, then `#<id> from <sender>, pane <id>, <HH:MM> [read]: <first line, 80 runes>`. With nothing to show it prints "no unread messages" (or "no messages" with `-all`).

## Follow mode

Polls every 2 seconds. Up to 3 new agent messages print individually; more become one digest line. Agent output is spaced at least 5 seconds apart and stops after 100 lines, after which only messages from the user are announced. Messages from the user print at once and are never folded. The stream ends when the pane is gone, or after 2 minutes of no answer from the instance; both are printed as lines and exit 0.
