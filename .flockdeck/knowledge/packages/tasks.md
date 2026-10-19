---
type: Package
title: tasks
description: The per-project task board, its state machine and permission rules, and the notices a change sends.
resource: internal/tasks
tags: [tasks, board, state-machine, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/tasks/tasks.go, internal/tasks/board.go, internal/tasks/exclude.go, internal/tasks/format.go, internal/tasks/notify.go, internal/tasks/rules.go]
---
# internal/tasks

The shared task board of a project (`flockdeck task`). It holds no panes or sessions: the workspace says who is acting and who is on the roster, and sends the messages `Notices` describes.

## Main pieces

| Piece | Notes |
|---|---|
| `Task`, `State`, `Actor`, `Note`, `Event` | [task data](/data/task.md) |
| `Board`, `Open(root)` | File-backed board at `.flockdeck/team/tasks.json` in the project's main checkout ([task board file](/data/task-board.md)). Methods: `Add`, `List`, `Get`, `Assign`, `SetState`, `AddNote`, `SetBranch`, `SetBaton`, `Prune`, `Counts` |
| `Transitions`, `Find`, `Next`, `Moves`, `CheckMove`, `CheckAssign`, `CheckEdit`, `CheckPrune` | Rule table; see [task permissions](/security/task-board-permissions.md) |
| `Notices(change, actor, members)` | Who is told what after a change. Text is built from the title and cleaned labels, never from a note's words |
| `ResolveAssignee` | Target is a role, a name or a pane id |
| `EnsureExcluded`, `MainCheckout` | Adds `.flockdeck/` to the repository's `info/exclude` (not `.gitignore`) the first time the file is written |

## Limits

Title 200 characters, body 16 KiB, note 4 KiB, 100 notes and 50 history entries per task, branch 200 bytes, 2000 tasks per board (the oldest done or cancelled task is dropped to make room; refused only when all are open). `DefaultPruneDays` is 30.

## Depends on

[team](/packages/team.md) (labels, roster), [store](/packages/store.md) (`ReadState`, `WriteAtomic`). Exposed to panes through [task endpoint](/api/hooks-task.md).

See [task board file recovery](/gotchas/task-board-file-recovery.md).
