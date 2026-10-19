---
type: Package
title: session hook events and status mapping
description: Which Claude Code hook events a pane subscribes to by version, and how StatusForEvent and ApplyEvent turn events into a pane status.
resource: internal/session/claude.go
tags: [session, hooks, status, claude]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/claude.go, internal/session/turn.go, internal/session/status.go, internal/session/session.go]
---
# Hook events and status mapping

## Subscription by version

`hookEventsFor(version)` returns the events written to the pane's settings file ([pane settings file](/data/pane-settings-file.md)).

- Always: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Notification`, `Stop`, `SessionEnd`.
- Added when `claude --version` reports 2.1.269 or later (`laterHooksSince`): `PermissionRequest`, `PostToolUseFailure`, `StopFailure`, `PermissionDenied`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`.

`versionAtLeast` reads the first field, accepts a pre-release suffix on a number, and treats anything unreadable as too old. The version is asked once per executable (3 second timeout, `versionTimeout`) and cached. A failed answer is cached for one minute (`versionRetry`), so a broken install does not cost every pane start the timeout. An unknown version subscribes to the base seven.

## StatusForEvent

`StatusForEvent(event, tool, notificationType)` returns a status, a detail and whether to apply it.

| Event | Result |
|---|---|
| `UserPromptSubmit`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied` | working |
| `PreToolUse` | working with the tool name; `AskUserQuestion` gives waiting |
| `Notification` | waiting with the tool, unless `notification_type` is `idle_prompt`, which changes nothing |
| `PermissionRequest` | waiting, no tool name (the tool from the `PreToolUse` stays) |
| `Stop`, `StopFailure`, `Interrupted` | idle |
| `SessionStart`, `SessionEnd`, unknown | no change |

`SessionStart` is not applied here because it fires again on compaction, mid-turn. `SessionEnd` is not applied because only the process reader knows the process has gone.

## ApplyEvent layers

`Session.ApplyEvent` (`turn.go`) runs, in order: `CompactionStatus`, `IsStaleNudge`, `StatusForEvent`, then a turn check.

- `CompactionStatus`: `PreCompact` with trigger `manual` shows working with detail `/compact`; `PostCompact`, or a `SessionStart` with source `compact`, ends it. An automatic compaction changes nothing.
- `IsStaleNudge`: a `Notification` with no tool and no type, on an idle pane whose hooks have been seen and with no background work, is an old Claude Code's idle nudge and is dropped.
- Turn state is `unknown`, `open` (after `UserPromptSubmit`) or `closed` (after `Stop`, `StopFailure`, `Interrupted`, an idle nudge, or a `clear` start). Once closed, a `PostToolUse` without a prior `PreToolUse` is dropped, and a subagent's tool calls (events carrying an agent id) do not flip the pane to working. A subagent's question or permission prompt still shows. Unattributed late `PreToolUse` events are counted in `strayTools` and the pane returns to its settled status when they finish.
- The first `SessionStart` marks `hooksSeen` and sets idle, after which output no longer implies work.
- `PermissionDenied` records a blocked tool; a `Stop` or `StopFailure` while one is recorded gives `blocked` instead of idle. `PostToolUse`, `UserPromptSubmit`, `SessionStart` and `Interrupted` clear it (`resolveBlockedLocked`).

Statuses and symbols (`status.go`): starting, working, waiting, blocked, idle, exited. `NeedsAttention` is waiting or blocked.

Related: [session](/packages/session.md), [hook events](/data/hook-event.md), [hook endpoint](/api/hooks-hook.md), [idle probe fallbacks](/gotchas/idle-probe-fallbacks.md).
