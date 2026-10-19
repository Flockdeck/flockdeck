---
type: Data Type
title: workspace.Pane
description: In-memory pane record, with which fields are persisted and which are runtime only.
resource: internal/workspace/workspace.go
tags: [workspace, panes]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/workspace.go]
---
`workspace.Pane` couples a live `session.Session` with what is needed to restart it.

## Persisted (see [layout file](/data/layout-file.md))

`ID`, `Kind`, `Cwd`, `Name`, `Agent`, `Model`, `Routed`, `RoutedFrom`, `RoutedFromAgent`, `Task`, `BatonID`, `Role`, `Root` (only when it differs from the tab's), `Conversation` (only when it differs from `ID`), `Cols`/`Rows`, `Parent`, `Locked`, `Recording`, `AutoReview`.

## Runtime only

| Field | Meaning |
|---|---|
| `Branch`, `Git`, `GitTimedOut` | Periodically refreshed status of the checkout; a timed-out refresh keeps the old value but flags it stale |
| `Conflicts` | Other panes whose work would conflict (radar; empty while the radar is off) |
| `Sess`, `Err` | Live session; start failure kept to show in place |
| `initial`, `batonPrompt` | Opening prompts, used once; a restart resumes instead |
| `launch` | Id of the current process start; hooks from an earlier start are dropped (env `hooks.LaunchEnv`) |
| `Ancestors` | Panes that started this one, for the helper cap even after a parent closes |
| `Muted`, `PeerName`, `AutoApproved` | Not persisted; a restarted pane is a new process |
| `exitReported`, `turnSeen`, `idleArmed` | Report a helper's exit and idleness to its parent once (idle must hold for 5 s) |

Semantics: empty `Agent` means the catalog default; empty `Model` means whatever the agent is already set to, which is not the same as naming its default model. `IsAgent()` is "not a shell". `Failed()` reports a non-zero exit or kill.

## Tab

`Tab` has `Delegated` (a spawn put a helper there; not persisted), `Spawned` (made by `flockdeck spawn -tab`, the only tabs a later `-tab` of the same title joins), `AutoTitle` (title still the directory name, so a first prompt may rename it) and `Named` (title chosen by hand; the automatic title is kept behind it so giving up the name restores it).

See [workspace](/packages/workspace.md).
