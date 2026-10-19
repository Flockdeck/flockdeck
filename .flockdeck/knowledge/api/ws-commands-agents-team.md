---
type: WebSocket Message
title: Control commands: agents, keys, batons, team, recording
description: Commands on /ws/control for agent catalog, API keys, batons, routing, the team graph, recordings and pane info.
resource: internal/server/agents.go
tags: [websocket, control, agents, team]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/agents.go, internal/server/baton.go, internal/server/control.go, internal/server/viewerrole.go, internal/server/teamui.go, internal/server/paneinfo.go, internal/server/spawn.go, internal/server/keys.go, internal/server/jevkey.go, internal/server/approval.go, internal/server/routing.go, internal/server/keybindings.go, internal/server/recording.go]
---
Part of [/ws/control](/api/ws-control.md). All `accessFull`.

| Family | `cmd` | Reply message | File |
|---|---|---|---|
| Agent catalog | `agents`, `refreshAgents`, `setAgentDefault`, `setAgentAddress` | `agents`, `agentAddress` | `agents.go` |
| Start | `startAgent` (`agent`, `model`, `role`, `task`, `worktree`, `root`) | `agentStarted` {paneId, id}, or a notice | `spawn.go` |
| API keys | `keys`, `keySet` (`id`, `text`), `keyClear` | `keys` (status only, never values) | `keys.go` |
| Jev key | `jevKey` (`kind`, `text`) | `jevKey` | `jevkey.go` |
| Batons | `makeBaton` (`id`, `req`), `saveBaton`, `startFromBaton` (`branch`, `confirmed`), `restartWithBaton`, `approveBaton` (`text` = token) | `batonDraft`, `batonSaved`, `batonError` | `baton.go`, `approval.go` |
| Routing | `setRouting`, `clearRoutingLog` | prefs | `routing.go` |
| Keybindings | `setKeybinding`, `resetKeybinding`, `resetKeybindings` | `keyTable` | `keybindings.go` |
| Pane info | `paneInfo`, `findPane` (`text`) | `paneInfo`, `paneMatches` | `paneinfo.go` |
| Recording | `recordPane` (`recording`, `confirmed`), `exportTranscript`, `revealTranscript`, `openRecordings` | notices | `recording.go` |
| Reveal | `revealPane` (`root`, `node` = tab id, `id` = pane) | focuses the pane in its current tab; notice if the pane is gone | `agents.go` |
| Team graph | `teamSnapshot`, `teamWatch`, `teamUnwatch`, `taskList`, `taskGet`, `taskAdd`, `taskAssign`, `taskStatus`, `taskNote`, `taskBranch`, `taskPrune`, `msgList`, `msgSend`, `msgSeen`, `teamClose` | `team`, `tasks`, `messages`, `teamResult` | `teamui.go` |

Notes:

- `startAgent` is rate limited per window (`startAgentLimit`).
- `recordPane` with `recording:true` needs the prefs flag `recordingAcknowledged`; otherwise the first call must carry `confirmed:true`. Recording is checked again on the server so a client that skips the question cannot skip the answer.
- Team commands are all refused from a relay window: `teamCommand` answers with a `teamResult` error and a notice.
- A baton to another provider needs a window approval, see [baton approval](/security/baton-approval.md).
