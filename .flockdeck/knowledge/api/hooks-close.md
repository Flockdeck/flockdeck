---
type: HTTP Endpoint
title: POST /close
description: A pane asks the application to close another pane, or every finished pane (flockdeck close).
resource: internal/hooks/hooks.go
tags: [hooks, close, panes]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# POST /close

Client: `hooks.Close(api, token, pane, req)`, 15 second timeout (longer than peer-name because `finished` can tear down panes across every open project).

## Request (`CloseRequest`, at most 1 MiB)

| Field | Meaning |
|---|---|
| `pane` | Caller's pane id |
| `target` | Pane to close; required unless `finished` is set (400 otherwise) |
| `finished` | Close every idle or exited pane |
| `force` | Close `target` even while it is working. Without it a pane that is not idle or exited is refused. Meaningless with `finished` |
| `token` | Run token |

A locked pane is never closed, whatever flags are passed.

## Response

200 JSON `CloseResult{closed, panes, tabs, locked}`: `closed` for a single close; `panes` and `tabs` counts for `finished`; `locked` is how many finished panes were kept open because they are locked. Errors from the handler (unknown pane, still working without `force`, closing itself) are 400 with text. 403, 405, 503 as for the other routes.
