---
type: CLI Command
title: flockdeck task
description: The project's shared task board, used by the agents of a team.
resource: cli_task.go
tags: [cli, pane, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_task.go, internal/hooks/tasks.go, internal/tasks/tasks.go]
---
## Synopsis

`flockdeck task <subcommand> ...`. Flags may appear before, between or after the positional arguments (`parseInterspersed`); everything after `--` is positional.

## Subcommands

| Subcommand | Flags | Effect |
|---|---|---|
| `add <title>` | `-body <text or ->` (at most 16 KB), `-role`, `-branch` | Adds a task in the planned state. Title at most 200 characters. |
| `list` | `-state <s or all>`, `-role`, `-mine`, `-json` | Open tasks, oldest first. Done and cancelled are hidden unless `-state all`. |
| `show <id>` | `-json` | Full task with body and notes. |
| `assign <id> <role|name|pane>` | none | Assigns the task. |
| `status <id> <state>` | `-note <text>` (at most 4 KB) | Moves the task. A note is needed to reopen a task or send it back from review. Not every move is allowed. |
| `note <id> <text...>` | none | Adds a note (at most 4 KB), or reads stdin when the text is `-`. Owner and creator are told. |
| `branch <id> <branch>` | none | Sets the git branch. |
| `baton <id> <baton-id>` | none | Attaches a [baton](/cli/baton.md). |
| `prune` | `-days N` (default 30, at least 1) | Removes done and cancelled tasks last changed more than N days ago. Manager role only. |

States: planned, assigned, in-progress, in-review, done, blocked, cancelled.

## Where it runs

Inside a pane; requires [FLOCKDECK_API](/config/flockdeck-api.md), [FLOCKDECK_TOKEN](/config/flockdeck-token.md) and [FLOCKDECK_PANE](/config/flockdeck-pane.md). Each subcommand is one `hooks.Task` call to the instance, which owns the board, so every pane in any worktree of the project sees the same one.

## Output

Changes print `<verb> <task line>` (added, moved, assigned, branch set on, baton set on, noted), a warning when a task is in review with no branch, any warning from the instance, and `told N pane(s)`. `prune` prints how many finished tasks it removed. No arguments prints the usage and exits 1; an unknown subcommand exits 1. `-h` exits 0.
