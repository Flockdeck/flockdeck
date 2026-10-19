---
type: WebSocket Message
title: /ws/control socket
description: The window's main WebSocket: JSON commands up, a state snapshot and typed replies down.
resource: internal/server/control.go
tags: [websocket, control, protocol]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go, internal/server/viewerrole.go, internal/server/ghcli.go, internal/server/server.go, internal/server/pty.go, internal/server/remote.go]
---
Handler: `Server.handleControl` (`internal/server/control.go`). Route `GET /ws/control`.

## Connection

- Auth: token, cookie or relay mark, else `403`.
- `websocket.Accept` with no origin patterns, so the page's `Origin` host must equal the request `Host` (see [origin check](/security/websocket-origin-check.md)). Through the relay: `CompressionContextTakeover`.
- Read limit 1 MiB per frame. Frames are text JSON.
- Per-connection `controlClient`: outbound queue of 64, a write loop with a 10 s write timeout, a keepalive ping every 30 s (`pingInterval`, timeout 15 s).
- The client is counted from the moment the socket opens, but is sent no state until its `hello` has been queued: the first outbound message is [`hello`](/api/ws-server-messages.md), then the first `state` snapshot. See [state ordering gotcha](/gotchas/hello-before-state.md).
- For relay connections the device id comes from the `Flockdeck-Remote-Device` header, set by the relay.
- Closing the last local window calls `OnLastClientGone`; relay windows never count towards that ([`LocalClientCount`](/gotchas/remote-windows-do-not-keep-app-alive.md)).

## Client to server: command envelope

One JSON object per frame, struct `command`, selected by `cmd`. Unknown JSON is dropped silently; each command runs under `guardFor` so a panic is reported to that window only.

| Field | Type | Used for |
|---|---|---|
| `cmd` | string | command name |
| `id`, `node`, `root`, `dir`, `kind`, `text`, `path` | string | generic target and payload; meaning depends on `cmd` |
| `cols`, `rows`, `size` | int | resize, font size, scrollback lines, rail width, push delay |
| `weights` | []float | split weights |
| `submit`, `force`, `push`, `split`, `follow`, `muted`, `locked`, `recording`, `confirmed`, `autoReview`, `purge`, `worktree` | bool | flags |
| `base`, `target`, `edge`, `agent`, `model`, `role`, `branch`, `task` | string | drag targets, agent start options |
| `relay`, `name`, `join`, `invite` | string | remote enable and rename |
| `files`, `omitted`, `stamps` | list, int, map | commit from review panel |
| `gitAccept`, `gitSeen` | string | answer to a `gitWarn` |
| `after`, `before`, `entry`, `q` | string | chat paging and search |
| `mediaType`, `data` | string | `attachImage` (base64) |
| `sha256` | string | helper install hash |
| `req` | string | baton request id echoed in the answer |
| `ghNumber`, `ghTitle`, `ghBody`, `ghBase`, `ghDraft`, `ghState` | mixed | GitHub panel |

## Dispatch

`handleCommand` first applies the [viewer gate](/security/viewer-command-gate.md) (`allowCommand`), then:

1. Commands handled directly (`setDeviceRole`, team, worktree, browse, baton, git, gh, conversation, remote, settings, helpers, recordings; project management).
2. Everything else is queued on the workspace goroutine in one big `switch`: tab, project, pane, layout and prompt commands, then `s.wakeAsked()` to push a fresh snapshot quickly.

Command tables by family:

- [workspace, tabs, panes, projects](/api/ws-commands-workspace.md)
- [git, review, worktrees, GitHub](/api/ws-commands-git.md)
- [chat, history, attachments](/api/ws-commands-conversation.md)
- [remote access and artifacts](/api/ws-commands-remote.md)
- [preferences, helpers, versions](/api/ws-commands-settings.md)
- [agents, keys, batons, routing, team, recording](/api/ws-commands-agents-team.md)

## Server to client

See [server messages](/api/ws-server-messages.md) and [state snapshot](/api/ws-state.md).
