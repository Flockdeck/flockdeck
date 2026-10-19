---
type: File Format
title: Team inboxes (messages.json)
description: Saved inboxes of messages between panes, written whenever an inbox changes.
resource: internal/workspace/team.go
tags: [workspace, team, persistence]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/team.go]
---
`messages.json` in the config dir. The contents are produced by `internal/team` (`Store.Snapshot`) and read back with `Store.Load`; this package only chooses the path and when to write.

- Loaded once at start by `loadMessages`. A missing or unparseable file leaves the inboxes empty. A read error other than not-exist skips loading and, because the change hook is installed after loading, saving too.
- Every inbox change wakes the window and writes the whole snapshot through `store.WriteAtomic` under `saveMu`, so an older snapshot is never written after a newer one. A failed write does not stop delivery; the in-memory inbox is what `flockdeck inbox` reads.
- A pane's inbox is kept when the pane is closed because Flockdeck quits or its project closes (the pane returns with the same id). It is deleted when the pane is closed on purpose, by the user or `flockdeck close` (`dropInbox`).
- Helpers report idleness to a parent only after being idle for 5 s (`helperIdleDwell`).

See [workspace](/packages/workspace.md), [state dir permissions](/security/state-dir-permissions.md).
