---
type: Package
title: proctree
description: Puts a started process in a job object (Windows) or a session (Unix) and ends it with everything it started.
resource: internal/proctree/proctree.go
tags: [process, windows, linux, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/proctree/proctree.go, internal/proctree/tree_unix.go, internal/proctree/tree_windows.go, internal/proctree/parents_linux.go, internal/proctree/parents_other.go, internal/proctree/parents_windows.go]
---
- `Start(cmd)` prepares (Setsid on Unix), starts and contains a command; `Prepare` and `Contain(pid)` are the two halves for a caller that starts the process itself, as `session` does through go-pty.
- `Tree.End(process, reaped, grace)` ends the process and what it started and waits for `reaped` to close within `grace`. A nil or zero Tree is safe; it falls back to the process table on Windows.
- `KillTree(pid)` is for a startup sweep that only has a recorded pid.
- `Parents()` and `SessionMembers(sid)` read the process table (/proc, Toolhelp); macOS has neither. `session` uses `Parents` for resource usage.

See [pane process-tree shutdown](/gotchas/pane-process-tree-shutdown.md) for why windowed programs are left running on Windows, and [session](session.md).
