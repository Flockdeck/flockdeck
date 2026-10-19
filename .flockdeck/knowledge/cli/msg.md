---
type: CLI Command
title: flockdeck msg
description: Sends a message to another agent in the same project.
resource: cli_team.go
tags: [cli, pane, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_team.go, internal/hooks/team.go, internal/team/store.go]
---
## Synopsis

`flockdeck msg send <target> <text...>`

`send` is the only subcommand. The text is the remaining arguments joined by spaces, or standard input when the text is `-`. It has no flags; `-h`, `-help` and `--help` print the usage and exit 0.

## Target

A pane id, a peer name or pane name, or a role id (every live pane in that role, excluding the sender). `role:<id>` disambiguates when a name and a role are spelled the same. A name shared by several panes is refused, with the panes listed.

## Limits

The text is trimmed and may be at most 8 KiB (`team.MaxText` = 8192 bytes). The command checks this before calling the instance and says to write a file and send its path instead.

## Where it runs

Inside a pane; requires [FLOCKDECK_API](/config/flockdeck-api.md), [FLOCKDECK_TOKEN](/config/flockdeck-token.md) and [FLOCKDECK_PANE](/config/flockdeck-pane.md).

## Output

For each recipient: `sent #<id> to <name> (pane <pane>)`, plus a warning line when the receiving agent has reported no hooks and may see the message only when it runs [inbox](/cli/inbox.md). Failures print `not sent: ...` on stderr; the exit status is still 0 unless the whole call fails. Wrong arguments print the usage and exit 1. Nothing is typed into the receiver's prompt.
