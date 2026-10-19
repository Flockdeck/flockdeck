---
type: Gotcha
title: A control socket gets hello before any state
description: A window is sent no snapshot until its hello is queued; the first state replaces any older pending one.
resource: internal/server/control.go
tags: [websocket, ordering, server]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go]
---
In `handleControl`, a client is registered in `s.clients` as "not greeted" the moment its socket opens, so it counts as a window and gets notices. Snapshots go only to greeted clients (`greetedClients`).

On the workspace goroutine the server sends `hello` (key table, prefs), marks the client greeted, then sends the first snapshot. Because broadcasts also run on that goroutine, none can reach the client before its hello. Before this ordering, about one connection in a few dozen received an already-queued snapshot ahead of its hello.

`writeLoop` drains everything already queued before writing the pending snapshot, so the key table and prefs reach the page before the first state, and a notice (the only place a refused worktree or failed commit is reported) does not wait behind a snapshot that a newer one would replace.

`lastState` (the last broadcast bytes) is reset when a window connects unless it is the only client or already identical, so a window never skips a change it was not told about.
