---
type: Package
title: team
description: Roster, inbox messages, mail notices and the derived team graph for the panes of one project.
resource: internal/team
tags: [team, messaging, inbox, graph]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/team.go, internal/team/block.go, internal/team/graph.go, internal/team/notify.go, internal/team/store.go]
---
# internal/team

Lets the panes of one project know about each other and talk. Holds no panes, sessions or files: the workspace builds the roster, stores what `Store.Snapshot` returns, and delivers what `Notifier` produces.

## Main pieces

| Piece | Notes |
|---|---|
| `Member`, `Lines`, `Resolve(members, target)` | Roster. A target is, in order: pane id, peer or pane name (case-insensitive), role id; `role:` forces a role. A name shared by several panes is an error listing them. A role fans out to every live pane except the sender |
| `Message`, `Sender`, `Store` | Inboxes: [team message](/data/team-message.md). `Send`, `SendSystem`, `List`, `Read`, `MarkSeen`, `Forget`, `Snapshot`, `Load`, `NewestUnread` |
| `Notifier`, `Notice`, `Reminder`, `FollowLine`, `FollowDigest` | Mail notices. Never contain message text |
| `PlainLabel`, `PlainText`, `AgentLabel`, `Impersonates`, `Block` | Sanitising of agent-chosen names and multi-line text; see [message sender spoofing](/security/team-sender-labels.md) |
| `BuildGraph(panes, tasks)` | [team graph](/data/team-graph.md) |

## Limits

`MaxText` 8 KiB per message, `MaxInbox` 100 per pane (a full inbox drops its oldest read message; refuses when all are unread; `SendSystem` drops the new one), `KeepRead` 7 days and `KeepUnread` 30 days across a restart, `MaxLabel` 64 runes, `Renotify` 5 minutes, `MaxReminders` 2.

## Used by

[hooks](/packages/hooks.md) (the roster and message routes), [tasks](/packages/tasks.md), the workspace. See [notices not typed](/gotchas/notices-not-typed.md).
