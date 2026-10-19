---
type: File Format
title: Task board file
description: Per-project tasks.json in the main checkout, written atomically and excluded from git.
resource: internal/tasks/board.go
tags: [tasks, board, file]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/tasks/board.go]
---
# Task board file

Path: `.flockdeck/team/tasks.json` relative to the project's main checkout. For a linked worktree, the board of the checkout it was cut from is used (`MainCheckout` finds it by reading `.git`, `commondir` and `gitdir:` files, without running git).

```json
{ "version": 1, "next": 4, "tasks": [ ... ] }
```

`tasks` are [Task](/data/task.md) objects; `next` is the next id number. Indented with two spaces.

## Writes

Every change reads the file, applies the change and writes it back under one lock, using `store.WriteAtomic`. The directory is created mode 0700. Reads use `store.ReadState` and a cache keyed by size and modification time (`Stamp`). When the file is first created, `.flockdeck/` is appended to the repository's `info/exclude` (not `.gitignore`) before the file is written, so a crash cannot leave a file git would offer to commit.

## Load rules

- Missing file: empty board.
- Unparseable file: renamed to `tasks.json.corrupt-<unix nanos>` and the board starts empty (so a team is not blocked), with a log line. See [recovery](/gotchas/task-board-file-recovery.md).
- `version` greater than 1: error, so an older build does not overwrite a newer file.
- Tasks with a non-positive or duplicate id are dropped on load.

Pruning (manager only) removes done or cancelled tasks last updated more than N days ago (default 30).
