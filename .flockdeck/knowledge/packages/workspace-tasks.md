---
type: Package
title: workspace task operations
description: How the workspace runs `flockdeck task` operations for a pane or for the person at the window, and which messages a change sends.
resource: internal/workspace/tasks.go
tags: [workspace, tasks, board, inbox]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/tasks.go]
---
# Workspace side of the task board

The board, its rules and its file live in [tasks](/packages/tasks.md) ([task board file](/data/task-board.md)). `internal/workspace/tasks.go` decides who is acting and which project's board is meant, then runs the operation.

## Which board

`boardOf(root)` picks the project's board:

1. If the root belongs to a project group with a primary checkout, the primary is used.
2. The key is `pathKey(tasks.MainCheckout(root))`, so every worktree of a repo reaches one board.
3. `taskState.boards` caches one `*tasks.Board` per key, under a mutex, so two projects that reach the same file share a lock. `taskState.open` defaults to `tasks.Open` and tests replace it.

## Who is acting

`taskActor(pane)` finds the pane in its roster (`Roster`) and builds a `tasks.Actor` from its pane id, the role it was started with and its label. A pane that is not an open agent pane is refused: "it is not part of the team and cannot use the task board". The role cannot be changed by what the caller sends.

## runTask

`runTask(actor, members, board, req)` is the single place operations are carried out, for panes (`Task`) and for the window ([workspace team UI](/packages/workspace-team-ui.md)).

| Op | Behaviour |
|---|---|
| list | Filter by state (`all` widens to finished), role, `Mine`; returns tasks and a hidden count |
| show | Needs a valid id (`tasks.ParseID`) |
| add | A role, if given, must exist in the catalog (`CheckRole`) |
| assign | `tasks.CheckAssign` runs before the target is resolved, so a pane that may not assign learns that first; the target resolves against the roster and role ids |
| status | `tasks.ParseState`, then `SetState` with an optional note |
| note, branch | `AddNote`, `SetBranch` |
| baton | The id must pass `baton.ValidID`, and its file must exist on this machine when a path can be worked out |
| prune | Days default to `tasks.DefaultPruneDays` |

An unknown op is an error with the name cleaned by `team.PlainLabel`. `hasManager` comes from `tasks.HasManager(members)`: a pane with no role has manager powers only while no manager-role pane runs.

## Notices

After a change, `sendTaskNotices` writes each message from `tasks.Notices(change, actor, members)` into the target pane's inbox with `SendSystem` and reports the count as `Told`. If a change overrode an in-progress owner, the result carries a warning to assign before the owner starts the task.

`TaskCounts(pane)` gives the briefing its snapshot ([pane briefing](/packages/workspace-briefing.md)); it reports false for a non-agent pane or an unreadable board.

All of this runs on the workspace goroutine ([goroutine rule](/gotchas/workspace-goroutine-only.md)).
