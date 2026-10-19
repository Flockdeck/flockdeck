---
type: Package
title: workspace
description: The mutable application state: open projects, tabs, pane trees and live sessions, plus spawning, persistence, messaging, recording and git refresh.
resource: internal/workspace/workspace.go
tags: [workspace, panes, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/workspace.go]
---
No rendering or key handling; it can be exercised without a UI. `workspace.New(Options{Root, OnWake, HookBinary})` resolves the root, creates the hook server panes report to, sweeps settings and temp files older than 24 h (`store.SweepSessions`), reaps stale worktree processes, and uses the generated settings dir (`store.SessionsDir`).

## Model

| Type | Notes |
|---|---|
| `Workspace` | All tabs across all open projects (`Tabs`), `panes` map, active root/tab, broadcast set, project groups, team and task state, recording manager, agent catalog, cached project metadata |
| `Tab` | id, `Root` (project), title, `layout.Node` tree, focus, zoom; flags `Delegated`, `Spawned`, `AutoTitle`, `Named` |
| `Pane` | id, kind, cwd, name, agent, model, routing fields, `Root`, `Branch`, `Git` status, `Conflicts`, size, `Sess`, task, `BatonID`, `Role`, `Parent`, `Locked`, `Recording`, `AutoReview`, ... see [workspace pane](/data/workspace-pane.md) |
| `Project` | root, display name, active, tab/pane counts, waiting/working tallies, `Activity`, `Members`, `Archived` |

A tab belongs to a project and so does a pane, but the two can differ: a tab may show agents from several projects. `rootOf(pane)` is what decides what the agent is told it works in, who counts it as waiting, and what stops it when a project closes.

## Areas

- Persistence and restore: [layout file](/data/layout-file.md), [restore rules](/gotchas/layout-restore-rules.md). `SaveAll`, `SaveLayouts`, `SaveProject`, `Restore`, `RestoreSession`.
- Spawn and worktrees: [spawn and worktree handling](/gotchas/worktree-spawn-and-cleanup.md), [helper limits](/security/spawn-approval-and-helper-limits.md).
- Activity: `PaneActivity` is the only place a pane's displayed activity is decided (waiting, blocked, working, idle, starting, exited, failed). Idle agents with verified background work read as working. `Aggregate` ranks waiting/blocked above working above none; `Tally` counts them.
- Team messages and tasks: [messages file](/data/messages-file.md); the board itself lives in `internal/tasks`, messages in `internal/team`. A pane is never typed into to announce mail; the notice rides the reply to its next hook (`HookNotice`).
- Recording: `SetPaneRecording`, `ExportTranscript`; follows the stored conversation with `record.Sync`, looking again 1.5 s after the last hook event, doubling, and stopping after 3 empty looks. See [record](/packages/record.md).
- Background work verification: [background work checks](/gotchas/background-work-verification.md).
- Conflict radar: [radar behaviour](/gotchas/conflict-radar-limits.md), [snapshots](/data/radar-snapshot.md).
- Provider and gateway detection: [provider detection](/security/provider-gateway-detection.md).
- Git refresh: `RefreshGit` reads each checkout's status with a 10 s deadline (`gitDeadline`), overlapping refreshes of one checkout collapse into a re-read.
- Rearranging (`move.go`): `MovePane`, `SwapPanes`, `MovePaneToTab`, `TilePanes`, `MergeTab`, `MoveTab` move live sessions without restarting them.
- Project groups: [groups](/data/groups-file.md); `ensureGroup` makes every open root a group of one by default.
- Broadcast, auto-review (`SetPaneAutoReview`), lock (`SetPaneLocked`), mute (`SetPaneMuted`, not persisted), peer name (`SetPanePeerName`, not persisted).
- Pane environment: `FLOCKDECK_API`, `FLOCKDECK_TOKEN`, `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME`, `FLOCKDECK_PROJECT` (each also under a legacy `PERCH_` name), plus `FLOCKDECK_AGENT`, `FLOCKDECK_MODEL`, `FLOCKDECK_ROLE` when applicable. See [FLOCKDECK_PANE](/config/flockdeck-pane.md).

Depends on [session](/packages/session.md), [layout](/packages/layout.md), [store](/packages/store.md), [gitx](/packages/gitx.md), [record](/packages/record.md), [hooks](/packages/hooks.md).
