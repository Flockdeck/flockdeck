---
type: Security Boundary
title: Viewer command and message gate
description: Default-deny filtering of control commands, outgoing messages, snapshots and terminal input for viewer devices.
resource: internal/server/viewerrole.go
tags: [security, remote, roles]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/viewerrole.go, internal/server/control.go, internal/server/conversation.go]
---
Role data lives in prefs ([device access roles](/security/device-access-roles.md)). This file's enforcement:

| Point | Rule |
|---|---|
| Incoming command (`allowCommand`) | Local windows and full devices may send anything. A viewer may send only commands that `commandAccess` marks `accessViewer` (none today) or, with `watchPanes`, `accessWatcher` (`conversationOpen`, `conversationOlder`, `conversationDetail`, `conversationClose`, `conversationSearch`). Unknown commands are refused. Refusal sends the notice "This device can only view..." |
| Outgoing message (`mayReceive`) | A viewer gets only `hello` and `artifacts`, plus `conversationPage`, `conversationDetail`, `conversationAppend`, `conversationSearch` if it may watch. Any later new type is dropped by default |
| Snapshot (`viewerSnapshot`) | Empty projects, tabs and panes unless `watchPanes`; then panes without desk-only parts |
| `hello` | A viewer gets empty prefs and its `role` and `watchPanes` |
| Terminal socket | Viewer without `watchPanes`: `403`. With it: output only; input, resize, focus discarded |
| Role changes | `setDeviceRole` (desk only) ends that device's open sockets so the window reconnects into the new role |

The role is looked up on every command, message and keystroke, not once when the socket opens. A request through the relay with no device header is treated as a viewer. `TestEveryCommandHasAnAccessDecision` reads `handleCommand`'s source and fails if a command is missing from `commandAccess`, so each new command needs an explicit decision.

Desk-only actions refused to full relay devices include (the list is not complete; for example turning Jev terminal output on from a relay window is also refused in `control.go`): quit, restart, detach, team, versions, updates, helpers, role changes, verify, `pair host`.
