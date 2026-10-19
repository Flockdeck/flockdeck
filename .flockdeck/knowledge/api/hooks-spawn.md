---
type: HTTP Endpoint
title: POST /spawn
description: A pane asks the application to start another agent pane (flockdeck spawn).
resource: internal/hooks/hooks.go
tags: [hooks, spawn, roles, baton]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# POST /spawn

Client: `hooks.Spawn(api, token, parent, req)`, used by `flockdeck spawn`.

## Request (`SpawnRequest`, JSON, at most 1 MiB)

| Field | Meaning |
|---|---|
| `parent` | Calling pane id (set by the client) |
| `task` | Opening task |
| `branch` | Branch for a worktree; the application decides the directory |
| `split` | Split the caller's tab |
| `tab` | Tab title to join, made if absent in the caller's project. Wins over `split`. At most 60 runes, no control or format characters (`CleanTabName`) |
| `shell` | Start a shell, not an agent |
| `agent`, `model` | Agent and model; both empty means the project default |
| `role` | Role id; fills agent and model only where the request names none; the caller's role must be allowed to start it ([agent role](/data/agent-role.md)) |
| `record` | Record the transcript; honoured only once the user has turned recording on |
| `baton` | Reference, never text: `self`, a pane id, a baton id, or `path:<absolute path>` |
| `batonElsewhere` | Allow the baton to go to an agent of another company; the user is asked in a window |
| `token` | Run token |

## Response

200 JSON `{paneId, cwd, role, batonId, batonPath}` (`paneId` required, else the client reports an error). Errors from the handler are 400 with the message as text. 403 bad token, 405 wrong method, 503 no handler.

## Timing

The client waits 60 s (`spawnTimeout`), plus 45 s (`BatonApprovalWait`) when `batonElsewhere` is set. A timeout message tells the caller the helper may still be starting: see [spawn timeout](/gotchas/spawn-timeout-is-not-failure.md).
