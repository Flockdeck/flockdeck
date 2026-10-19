---
type: Package
title: store
description: On-disk persistence of layouts, projects, session, preferences, instance record and related state under the per-user config dir.
resource: internal/store/store.go
tags: [store, persistence]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
All state lives in `<os.UserConfigDir>/flockdeck` (`store.Dir`, 0700). Only the workspace shape is saved; conversations stay in the agent's own store.

## Files

| File | Concept |
|---|---|
| `layout-<hash>.json` | [layout](/data/layout-file.md) |
| `projects.json` | [recent projects](/data/projects-file.md) |
| `session.json` | [open projects](/data/session-file.md) |
| `groups.json` | [project groups](/data/groups-file.md) |
| `prefs.json` | [preferences](/data/prefs-file.md) |
| `instance.json`, `start.lock` | [instance record](/data/instance-file.md) |
| `worktree-procs.json` | [worktree processes](/data/worktree-procs-file.md) |
| `git-programs.json` | [accepted git programs](/data/git-programs-file.md) |
| `sessions/*.settings.json` | generated per-pane agent settings, swept by `SweepSessions` |

## Main API

`Dir`, `SessionsDir`, `Load`/`Peek`/`Save`, `Recents`/`TouchRecents`/`ForgetRecent`/`SetProjectName`/`SetProjectArchived`/`ReorderProjects`, `LoadSession`/`SaveSession`, `LoadGroups`/`SaveGroups`, `LoadPrefs`/`ReadPrefs`/`SavePrefs`, `LoadInstance`/`SaveInstance`/`ClearInstance`, `TryLockStart`, `TakeKept`, `WriteAtomic`, `ReadState`.

Used by [workspace](/packages/workspace.md) and the server and CLI layers. Related: [state dir permissions](/security/state-dir-permissions.md), [device roles](/security/device-access-roles.md), [set-aside files](/gotchas/unreadable-state-files-are-set-aside.md), [atomic writes](/gotchas/atomic-state-writes-and-windows-contention.md), [legacy dir](/gotchas/state-dir-renamed-from-perch.md).
