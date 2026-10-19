---
type: WebSocket Message
title: Control socket server messages
description: Every message type the server sends down /ws/control, with its source and who receives it.
resource: internal/server/control.go
tags: [websocket, control, protocol]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go, internal/server/viewerrole.go, internal/server/baton.go, internal/server/conversation.go, internal/server/help.go, internal/server/approval.go, internal/server/agents.go, internal/server/remote.go, internal/server/imageattach.go, internal/server/ghcli.go, internal/server/teamui.go, internal/server/paneinfo.go, internal/server/spawn.go, internal/server/artifacts_consent.go, internal/server/keys.go, internal/server/jevkey.go, internal/server/browse.go, internal/server/changes.go, internal/server/gitprograms.go, internal/server/worktrees.go, internal/server/history.go, internal/server/conversation_search.go, internal/server/helpers_ui.go, internal/server/versions.go]
---
Each message is a JSON object with a `type`. Sent by `controlClient.send`, which drops it unless `mayReceive` allows it; a viewer device receives only `hello` and `artifacts`, plus four chat types when it may watch panes ([gate](/security/viewer-command-gate.md)). The `state` snapshot is separate ([details](/api/ws-state.md)).

| `type` | Source | Content |
|---|---|---|
| `hello` | `help.go` | `keys` (key table with `overridden`), `prefs`, `remote` (bool), `radarUnavailable`, `radarChecking`, `e2ePublicKey` (relay windows only), `artifacts` {v}, `role`, `watchPanes`. First message sent |
| `state` | `control.go` | snapshot, see [state](/api/ws-state.md) |
| `notice` | `control.go` | `text`, `error` (bool), optional `kind` (`approval`), and for approvals `lead`, `action`, `lifeMs`, `pin`, `id` |
| `noticeWithdraw` | `approval.go` | `id` of an approval notice to remove |
| `prefs` | `control.go` | whole prefs, per window (`prefsFor`; relay windows get no artifact settings) |
| `keyTable` | `help.go` | whole key table after a remap |
| `radarSupport` | `help.go` | `unavailable` reason once git's merge-tree support is known |
| `artifacts` | `artifacts_consent.go` | `available`, `v`; tells a relay window artifacts were switched on or off for its device |
| `detached` | `control.go` | reply to `detach` |
| `agentStarted` | `spawn.go` | `paneId`, `id` |
| `agents`, `agentAddress` | `agents.go` | agent overview, address change |
| `keys` | `keys.go` | API key status list |
| `jevKey` | `jevkey.go` | whether a key env var or stored key exists |
| `browse`, `recents` | `browse.go` | folder listing, recent projects |
| `changes`, `diff`, `gitWarn` | `changes.go`, `gitprograms.go` | review panel |
| `worktrees` | `worktrees.go` | worktree list |
| `ghStatus`, `ghProgress`, `ghPRs`, `ghPR`, `ghIssues`, `ghIssue`, `ghChecks` | `ghcli.go` | GitHub panel |
| `conversations`, `conversationPage`, `conversationDetail`, `conversationAppend`, `conversationSearch` | `history.go`, `conversation.go`, `conversation_search.go` | chat and history |
| `attachImageResult` | `imageattach.go` | `id`, `path`, `error` |
| `paneInfo`, `paneMatches` | `paneinfo.go` | pane field list, search results |
| `batonDraft`, `batonSaved`, `batonError` | `baton.go` | baton results, tagged with the request's `req` |
| `team`, `tasks`, `messages`, `teamResult` | `teamui.go` | team panel |
| `remoteDevices`, `remotePair`, `remoteOutcome` | `remote.go` | remote dialog |
| `helpers`, `helperPlan` | `helpers_ui.go` | helper apps |
| `versions` | `versions.go` | installable versions |

Notices are not state: a window refused a command by the desk gets a `notice` even when it would otherwise be sent no notices (`refuseViewer` bypasses the filter).
