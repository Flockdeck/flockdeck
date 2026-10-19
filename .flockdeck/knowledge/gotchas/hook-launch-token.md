---
type: Gotcha
title: Late hook events after a pane restart
description: A restarted pane keeps its id, so each process start carries a launch marker to tell old hooks from new ones.
resource: internal/hooks/hooks.go
tags: [hooks, windows, race]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
A restarted pane keeps its id. A hook that the previous process ran a moment too late, which on Windows can outlive the process that ran it, would otherwise read as an event about the new process.

Each start of the pane sets `FLOCKDECK_LAUNCH` to a new value ([env](/config/flockdeck-launch.md)). `Emit` sends it as `Event.Launch`; the workspace drops an event when its `Launch` is non-empty and differs from the pane's current one (`workspace.go`). An empty value, from a hook that predates the field, is let through.

Related ordering guard: `Emit` returns before posting when a `Notification` is one of the finished kinds, so those never turn a pane amber ([hook route](/api/hooks-hook.md)).
