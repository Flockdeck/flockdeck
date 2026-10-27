---
type: Package
title: workspace team panel operations
description: The workspace methods behind the window's Team panel: roster, board, messages from the person, and closing finished panes.
resource: internal/workspace/teamui.go
tags: [workspace, team, window, messages]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/teamui.go]
---
Direction (27 Oct 2026): delivery to a pane's inbox is planned for removal; see the mail decision. Status: planned removal. See docs/acp-direction.md.

# Team panel operations

`internal/workspace/teamui.go` is the person-at-the-window side of the team. It calls the same functions panes use (`runTask`, `deliver`, the board, the inbox store), so rules, limits and notices are one set. The person is no pane and has no role: tasks see `tasks.UserActor()` and messages `team.UserSender()`. Every method runs on the workspace goroutine.

## Methods

| Method | Effect |
|---|---|
| `TeamRoot(root)` | Resolves a directory to the root of an open project; empty means the project on screen. Errors with "no project is open at ..." |
| `TeamMembers(root)` | Agent panes of that project in tab order; none is marked `Self` |
| `TeamBoard(root)` | The project's `tasks.Board` |
| `UserTask(root, req)` | Runs a task op with the pane in the request cleared. Permissions are those of a manager-role pane, except the person is not among the panes told |
| `UserMessage(root, target, text)` | Delivers a message to a pane, name or role. The text never reaches a terminal; the pane gets the usual mail notice and reads it with `flockdeck inbox` |
| `TeamMessages(root)` | Every message in the project's agent inboxes, oldest first, plus the roster |
| `UserSeeMessage(root, pane, id)` | Sets the window's seen mark through `MarkSeen`. The pane must belong to the project. It does not mark the message read |
| `TeamMessageRev()` | A counter that changes when any inbox does |
| `CloseFinishedIn(root, ids)` | Closes finished agent panes in the list |

## CloseFinishedIn

At most 100 ids (`maxCloseAtOnce`). Each pane is judged again, because the window's list may be stale. It is skipped, with a reason in `skipped`, when it is not an agent pane of the project, is locked (`PaneLocked`), or is no longer finished (`paneFinished`: idle with no background work, or cleanly exited, the test `flockdeck close -finished` uses). Closed panes call `noteFinished`, which tells a helper's parent.

Related: [workspace task operations](/packages/workspace-tasks.md), [team](/packages/team.md), [team messages](/data/team-message.md), [close](/cli/close.md), [WebSocket team commands](/api/ws-commands-agents-team.md).
