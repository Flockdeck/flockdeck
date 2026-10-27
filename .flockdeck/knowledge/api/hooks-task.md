---
type: HTTP Endpoint
title: POST /task
description: Task board operations for flockdeck task, selected by an op field.
resource: internal/hooks/tasks.go
tags: [hooks, tasks]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/tasks.go]
---
Direction (27 Oct 2026): the task board is planned for removal (T203). Status: planned removal. See docs/acp-direction.md.

# POST /task

Same handler shape and checks as [team messaging](/api/hooks-team-messaging.md) (POST, 1 MiB, token, installed function, 400 with text on refusal). Client: `hooks.Task(api, token, pane, req)`, which refuses an empty `op`.

## Request (`TaskRequest`)

`Auth{pane, token}` and `op`, one of `add`, `list`, `show`, `assign`, `status`, `note`, `branch`, `baton`, `prune`. Which other fields are read depends on `op`:

| op | Fields |
|---|---|
| add | `title`, `body`, `role`, `branch` |
| list | `state`, `role`, `mine`, `all` |
| show, branch, baton | `id` plus `branch` or `baton` |
| assign | `id`, `target` (role, name or pane id) |
| status | `id`, `state`, `note` |
| note | `id`, `text` |
| prune | `days` (0 means 30) |

## Response (`TaskResult`)

`task`, `tasks`, `hidden` (finished tasks a list left out), `pruned`, `told` (panes messaged because of the change), `warning`.

Rules for who may do what are in [task permissions](/security/task-board-permissions.md); the board is [tasks](/packages/tasks.md).
