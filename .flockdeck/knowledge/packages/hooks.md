---
type: Package
title: hooks
description: Loopback HTTP server and clients that carry agent lifecycle events and pane commands back to the running app.
resource: internal/hooks
tags: [hooks, loopback, lifecycle, callbacks]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go, internal/hooks/notification.go, internal/hooks/tasks.go, internal/hooks/team.go, internal/hooks/usage.go]
---
# internal/hooks

Carries lifecycle events from panes to Flockdeck. It is also the callback API behind `flockdeck spawn`, `close`, `peer-name`, `team`, `msg`, `inbox` and `task`, which run inside a pane.

## Server

`Serve(on)` listens on `127.0.0.1:0` (random port) and makes a random 16-byte hex token. Every route is POST-only, reads at most 1 MiB, and compares the token in constant time ([token security](/security/hook-server-token.md)).

| Route | Doc |
|---|---|
| `/hook` | [hook event](/api/hooks-hook.md) |
| `/spawn` | [spawn](/api/hooks-spawn.md) |
| `/usage` | [usage](/api/hooks-usage.md) |
| `/peer-name` | [peer-name](/api/hooks-peer-name.md) |
| `/close` | [close](/api/hooks-close.md) |
| `/team`, `/msg`, `/inbox` | [team messaging](/api/hooks-team-messaging.md) |
| `/task` | [task board](/api/hooks-task.md) |

The application installs handlers with `SetContextHandler`, `SetNoticeHandler`, `SetReviewHandler`, `SetSpawnHandler`, `SetUsageHandler`, `SetPeerNameHandler`, `SetCloseHandler`, `SetTeamHandler`, `SetMsgHandler`, `SetInboxHandler` and `SetTaskHandler`. A route with no handler answers 503.

## Clients

`Emit` (reads Claude Code's hook JSON from stdin, posts it, 3 second timeout), `Spawn`, `PeerName`, `Close`, `Team`, `Msg`, `Inbox`, `ReadMessage`, `Task`, `Report`. [chat](/packages/chat.md) calls `Emit` with a nil stdin to report its own lifecycle.

## Other

- `Event` ([hook event data](/data/hook-event.md)), `ParseTaskNotification` (strict reader for Claude Code's `<task-notification>` block), `ValidTaskID`, `CleanTabName` (at most 60 runes, no control or format characters), `LaunchEnv`.
- Imports [record](/packages/record.md) (redaction), [spend](/packages/spend.md), [team](/packages/team.md) and [tasks](/packages/tasks.md).

See [launch token](/gotchas/hook-launch-token.md) and [notices not typed](/gotchas/notices-not-typed.md).
