---
type: File Format
title: Worktree process records (worktree-procs.json)
description: Pane processes running in git checkouts, so a later launch can reap ones it never started.
resource: internal/store/worktreeprocs.go
tags: [store, persistence, worktree]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/worktreeprocs.go, internal/workspace/reap.go]
---
`worktree-procs.json` in the config dir: a JSON object keyed by pane id, each value a `WorktreeProc` {`pid`, `started`, `path`}.

- `TrackWorktreeProc` / `UntrackWorktreeProc` / `LoadWorktreeProcs`, guarded by one mutex because a fan-out starts and closes many panes at once.
- Workspace records only a pane whose cwd is outside its own project root (a fan-out worktree sits beside the repository), so a record is a worktree pane by construction. After ending a stale process the reaper removes its folder unless git still lists it as a worktree.
- A launch reads the file once near start (`workspace.ReapStaleWorktreeProcesses`), before recording its own panes; a record whose pid is alive and whose start time matches (2 s slack) gets its process tree killed. The sweep is skipped while another instance is running (`-solo`).

See [store](/packages/store.md), [gitx](/packages/gitx.md).
