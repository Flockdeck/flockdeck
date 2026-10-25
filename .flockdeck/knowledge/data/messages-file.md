---
type: File Format
title: Team inboxes (messages.json)
description: Saved inboxes of messages between panes, written whenever an inbox changes.
resource: internal/workspace/team.go
tags: [workspace, team, persistence]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/team.go, internal/team/store.go, internal/store/store.go]
---
`messages.json` in the config dir. The contents are produced by `internal/team` (`Store.Snapshot`) and read back with `Store.Load`; this package only chooses the path and when to write.

- Loaded once at start by `loadMessages`. A missing or unparseable file leaves the inboxes empty. A read error other than not-exist skips loading and, because the change hook is installed after loading, saving too.
- Every inbox change wakes the window and writes the whole snapshot through `store.WriteAtomic` under `saveMu`, so an older snapshot is never written after a newer one. A failed write does not stop delivery; the in-memory inbox is what `flockdeck inbox` reads.
- A pane's inbox is kept when the pane is closed because Flockdeck quits or its project closes (the pane returns with the same id). It is deleted when the pane is closed on purpose, by the user or `flockdeck close` (`dropInbox`).
- Helpers report idleness to a parent only after being idle for 5 s (`helperIdleDwell`).

## Team event log (team-events.jsonl)

Next to `messages.json`, `loadEvents` keeps the team event log (`team.EventLog`): one JSON line for each message sent and each delivery state a message reaches (notified, woken, read). A line holds ids, panes, a state and a time, never message text. The one exception is a wake event for an agent the user opted in to text mode (`WakePrefs.TextAgents`, only for `Caps.TextWake` agents): its `line` is the cleaned text typed, up to 300 characters.

- It is one file for the whole config dir, shared by every project. It is not one log for each project.
- Ids are the inbox ids. If `messages.json` is lost, the ids start again from 1, so the same id can appear for two different messages in one log.
- Events are appended when the store reports them, which is not always the order of their times. A delivery event can come before the one it follows, so read the time on each line, not the position.
- It keeps at most 2000 lines and 256 KiB, dropping the oldest first. A damaged line is skipped on load.
- A `rename` event (a pane named another) has `from`, `to`, `old` and `new` and no id. Only the newest 200 are kept, so a burst of renames cannot push the others out. `from` is the pane id the command reported. A build from before labels skips that line when it loads the file.

See [workspace](/packages/workspace.md), [state dir permissions](/security/state-dir-permissions.md).
