---
type: Gotcha
title: The workspace is touched from one goroutine only
description: Every read or write of the workspace must go through Server.do or ask; direct access from a connection goroutine races.
resource: internal/server/control.go
tags: [concurrency, server, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go]
---
`workspace.Workspace` is not safe for concurrent use, but many connection goroutines reach it. `runLoop` owns it and runs closures queued on `Server.cmds` (capacity 64).

- `do(fn)` queues work; it silently drops the work once the server is closing.
- `ask(s, fn)` waits for the result; `ok` is false if the server closed or `fn` panicked, so callers must not wait forever.
- A closure taken off the queue after `Close` is dropped, so shutdown's final save does not race a late change.
- `/health` and `/open` hand work over with their own deadline instead of `do`, because `do` waits without a deadline and the queue fills exactly when the workspace is slow.
- Slow work (git, relay calls, file reads) runs on its own goroutine with `surviveFor`, and posts results back with `do`.
- A panic in a command is recovered (`guard`), logged to `error.log` and reported to the window, so one bad command does not kill every agent.
- Fields owned by the workspace goroutine (`lastState`, `unsavedAskedAt`, `teamSent`, `lastViewerState`) have no lock of their own.
- `Close` closes `s.closed` under `s.mu`, the same lock `handleControl` takes before `connWG.Add`, so `Wait` never races an `Add`.
