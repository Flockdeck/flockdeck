---
type: Gotcha
title: Closing a pane must end its whole process tree
description: Killing only the pane's process leaves children running and holding the worktree open, so each OS uses a session or job object.
resource: internal/proctree/proctree.go
tags: [session, windows, linux, shutdown]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/proctree/proctree.go, internal/proctree/tree_unix.go, internal/proctree/tree_windows.go, internal/session/kill.go]
---
- The code is in `internal/proctree`, which the session uses for a pane and any process started without a pane can use too (`proctree.Start`, then `Tree.End`); the ACP client uses it for an agent and the child its adapter starts.
- Unix: go-pty starts the pane as a session leader (`Setsid`). Close sends hangup to the session first (as a closing terminal would; `hangupGrace`), then kills. It targets the session, not the process group, because an interactive shell puts each job in its own group. Linux lists session members through /proc; on macOS only the pane's own group is signalled, so a shell job that ignores hangup can outlive the pane there. A process that made its own session on purpose is left alone.
- Windows (`tree_windows.go`): the process is put in a job object at start; `KillTree` ends console programs but not programs with windows of their own (a browser opened for a login, an editor). Killing one process alone left children running, and an agent started from a `.cmd` shim held its worktree folder open while the folder was being removed. `Close` waits up to 3 s for the process to be gone for the same reason.
- While a pane runs in a git checkout, workspace records its process in [worktree procs](/data/worktree-procs-file.md) so a later launch can reap leftovers.

See [session](/packages/session.md).
