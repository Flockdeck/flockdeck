---
type: Data Type
title: Team message and inbox snapshot
description: A message in a pane's inbox, the Member roster entry, and the JSON snapshot the inboxes are saved as.
resource: internal/team/team.go
tags: [team, messages, inbox]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/team.go, internal/team/store.go]
---
Direction (27 Oct 2026): the per-pane inbox is planned for removal; a small audit log of messages stays. Status: planned removal. See docs/acp-direction.md.

# Message

| JSON | Meaning |
|---|---|
| `id` | Integer, counts up across all inboxes |
| `from` | `Sender{pane, role, name, user}`. `user` marks the person at the window; a message with no pane, no user flag and name `flockdeck` is a system message |
| `to` | Receiving pane id |
| `time`, `text` | Text is at most 8 KiB and must not be blank |
| `read` | Set when the agent reads it with `flockdeck inbox` |
| `seen` | The window's own mark; the agent's inbox and unread count ignore it |

## Member (roster entry)

`pane`, `name`, `peer` (name the agent reported), `role`, `agent`, `model`, `status`, `self`. Status `exited` means the process is gone. `Label()` is the peer name if set, else the pane name, passed through `AgentLabel`.

## Snapshot

`Store.Snapshot()` returns `{"version": 1, "next": n, "boxes": {"<pane id>": [messages]}}`, indented. The caller decides where to keep it (the workspace). `Load` drops messages with a bad id, a `to` that does not match the box, text over the limit, read ones older than 7 days, any older than 30 days, and keeps only the newest 100 per box.

See [team](/packages/team.md) and [team sender labels](/security/team-sender-labels.md).
