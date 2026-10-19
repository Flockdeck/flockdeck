---
type: Package
title: sysproc
description: Starts background child processes without console windows on Windows, and reaps stale processes.
resource: internal/sysproc
tags: [windows, process]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/sysproc/doc.go, internal/sysproc/reap.go, internal/sysproc/sysproc_other.go, internal/sysproc/sysproc_windows.go]
---
- `NoWindow(cmd)`: on Windows sets `CREATE_NO_WINDOW` (0x08000000) in `SysProcAttr.CreationFlags`, keeping flags already set. No-op elsewhere. Needed because the Windows release is a GUI-subsystem binary, so each console child (git polls, cmd, agent run_command) would otherwise get a new console window.
- `Reap(procs, same, terminate)`: for each `StaleProc{ID, PID, Started, Path}`, ends the process only if PID > 0 and `same` confirms it is still the recorded one (PIDs are reused). Returns `killed` and `settled` (settled includes killed and already-gone). Terminate is injected; callers pass tree-killers.

Callers of `NoWindow`: appwindow, chat/tool, ghcli, gitx, helpers, idle, review, session, workspace. Callers of `Reap`: `internal/helpers` supervisor and `internal/workspace`.
