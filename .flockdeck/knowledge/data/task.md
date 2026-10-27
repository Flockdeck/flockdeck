---
type: Data Type
title: Task
description: One unit of work on a project's task board, with owner, state, notes and history.
resource: internal/tasks/tasks.go
tags: [tasks, board]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/tasks/tasks.go]
---
Direction (27 Oct 2026): the task board is planned for removal (T203). Status: planned removal. See docs/acp-direction.md.

# Task

| JSON | Meaning |
|---|---|
| `id` | `T` and a number that counts up for the life of the board. `ParseID` accepts `T3`, `t3`, `#3`, `3` |
| `title`, `body` | One-line title (200 characters), body (16 KiB) |
| `ownerRole`, `ownerPane`, `ownerName` | Role the task is for, the pane that has it, and the pane's name when assigned. A task for a role has no pane until one claims it |
| `creator` | `Actor{pane, role, name, user}` |
| `state` | See below |
| `branch` | Git branch the work is on |
| `baton` | Id of a [baton](/data/baton.md) that gives context; the task only points at it |
| `notes` | `{author, time, text}`, newest 100 kept |
| `history` | `{actor, time, what}`, newest 50 kept; `what` is a few words, never a note's text |
| `created`, `updated` | Times |

## States

`planned`, `assigned`, `in-progress`, `in-review`, `done`, `blocked`, `cancelled`. `done` and `cancelled` are finished. `ParseState` accepts any case and `_`, space or no separator, plus `canceled`.

## Moves

The allowed moves and who may make them are in [task permissions](/security/task-board-permissions.md). The `user` actor (the person at the window) has manager powers and no pane or role.
