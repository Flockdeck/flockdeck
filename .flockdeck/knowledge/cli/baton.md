---
type: CLI Command
title: flockdeck baton
description: Lists and prints batons, the handoff documents agents write for each other.
resource: cli_baton.go
tags: [cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_baton.go]
---
## Synopsis

- `flockdeck baton list`
- `flockdeck baton show [-path] <baton-id>`

## Subcommands

| Subcommand | Effect |
|---|---|
| `list` | One line per baton, newest first: id, creation time (local, `2006-01-02 15:04`), source agent (or "-"), title on one line. Takes no arguments. With none stored it prints how to make one and the folder. |
| `show <id>` | Prints the rendered baton. Marks it as used (`Touch`). Unknown id fails with "no baton ...; flockdeck baton list shows what there is". |

`show` has one flag:

| Flag | Default | Effect |
|---|---|---|
| `-path` | false | Print only the file the baton is kept in. |

## Where it runs

Any shell. Batons are files in the state directory ([baton package](/packages/baton.md)), so no running instance and no pane environment is needed. Output is passed through `baton.CleanText` so stored text cannot move the cursor.

## Exit behaviour

No arguments prints the usage and exits 1. `-h` exits 0. An unknown subcommand exits 1. [spawn](/cli/spawn.md) `-baton` starts a helper from a baton; [task](/cli/task.md) `baton` attaches one to a task.
