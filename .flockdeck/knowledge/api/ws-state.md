---
type: WebSocket Message
title: state snapshot
description: The whole-workspace snapshot broadcast to every window on /ws/control.
resource: internal/server/control.go
tags: [websocket, control, state]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go]
---
Struct `stateMsg` (`type: "state"`), built on the workspace goroutine by `snapshot()` and sent by `broadcastState`.

## Fields

| Field | Meaning |
|---|---|
| `root`, `activeTab`, `broadcast` | active project root, active tab id, broadcast-input mode |
| `claudeAvailable`, `waiting`, `working` | CLI on PATH, counts of panes by activity |
| `projects[]` | `root`, `name`, `active`, `tabs`, `waiting`, `working`, `activity`, `panes`, `members[]` (repo root and name), `archived` |
| `tabs[]` | `id`, `title`, `focus`, `zoom`, `attention`, `activity`, `named`, `delegated`, `root` (layout tree of `nodeView`) |
| `panes` | map pane id to `paneView`: `id`, `kind`, `name`, `cwd`, `branch`, `project`, `parent`, `role`, `status`, `activity`, `detail`, `statusSince`, `ask`, `permission`, `muted`, `locked`, `recording`, `unread`, `autoReview`, `autoApproved`, `background`, git summary and usage fields |
| `agents` | agent catalog: which agents exist, which can start here, defaults |
| `update`, `recall` | staged update, recalled-version notice |
| `remote` | tunnel state, see [remote commands](/api/ws-commands-remote.md) |
| `canStartAgent`, `canSearchConversation`, `canMutePane`, `canAutoReview` | capability flags; absent on an older desktop, which is how a phone knows to hide the offer |

## Delivery rules

- Pushed at most every 100 ms for agent chatter (`stateInterval`) and 5 ms after a change a person just asked for (`askedInterval`). An unchanged snapshot is not resent (`lastState`).
- Windows keep only the newest pending snapshot (`pending`): snapshots supersede each other, other messages queue in order.
- A usage rebuild every 5 s (`usageRefresh`) while a window is open; the result is sent only if the rounded figures differ.
- A viewer device gets `viewerSnapshot`: empty projects, tabs and panes, unless the desk allowed it to watch panes, in which case panes are included without the agent catalog, update and recall, relay address, other devices' viewers or the capability flags except `canSearchConversation`.
