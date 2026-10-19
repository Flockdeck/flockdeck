---
type: Gotcha
title: Background work counts only on evidence
description: An idle agent with background tasks reads as working only while the work is verified, and ends only on stored evidence.
resource: internal/workspace/background_check.go
tags: [workspace, session, status]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/background_check.go]
---
- `session.BackgroundUnverifiedGrace` (30 minutes) without a sign of life marks a task unverified. `PaneActivity` shows an idle agent as working only if `BackgroundVerified() > 0`. The header still counts and lists unverified work, and `PaneFinished` does not treat a pane with any counted work as finished.
- `DueBackgroundChecks` runs on the workspace goroutine and touches no file; `RunBackgroundCheck` reads the stored Claude Code conversation off that goroutine. First check 45 s after idle with work counted, doubling when nothing is found up to 8 minutes. The first read takes the last 8 MiB of the conversation, later reads at most 4 MiB each.
- Evidence that ends work (as Claude Code 2.1.28x records it): a task-notification with a stopped status; a subagent hand-back; a successful TaskStop/KillShell naming the task; a subagent transcript ending on its hand-back; a non-persistent Monitor past its `timeoutMs`. Evidence only counts if dated after Flockdeck first heard of the work and after the latest sign of running. An Agent call's own result is not an end (Claude Code answers `async_launched` at once).
- Signs of running: a subagent transcript or task output file written within 2 minutes.

See [session](/packages/session.md), [workspace](/packages/workspace.md).
