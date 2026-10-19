---
type: Gotcha
title: Task board corrupt-file and version handling
description: A damaged tasks.json is set aside and the board restarts empty, while a newer-format file blocks writes.
resource: internal/tasks/board.go
tags: [tasks, recovery, git]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/tasks/board.go]
---
- A `tasks.json` that does not parse is renamed to `tasks.json.corrupt-<unix nanos>` and the board starts empty, so a team that cannot stop is not blocked. If the rename itself fails the call returns an error.
- A file with `version` above 1 is an error, never replaced, so an older Flockdeck cannot clobber a newer one's data.
- Other processes may change the file between reads: every change re-reads, edits and writes under one lock, and a cache keyed by size and modification time is dropped on a change. Two processes writing in the very same moment can still lose an update (code comment).
- The first write adds `.flockdeck/` to the repository's `info/exclude` before the file exists. A directory that is not a git checkout is left alone with no error. Linked worktrees share the main checkout's board and exclude file.
- A full board (2000 tasks) drops its oldest finished task to take a new one; only a board of all open tasks refuses.
