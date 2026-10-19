---
type: HTTP Endpoint
title: POST /hook
description: Loopback route where panes report agent lifecycle events; its reply can carry a briefing, a mail notice or an auto-review allow.
resource: internal/hooks/hooks.go
tags: [hooks, lifecycle, loopback]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# POST /hook

Served by [hooks](/packages/hooks.md) on `127.0.0.1:<random port>`. The address is `Server.Endpoint()`; panes are told it and the token in their environment.

## Request

JSON body, at most 1 MiB: an [Event](/data/hook-event.md) plus `"token"`. Built by `hooks.Emit`, which reads Claude Code's hook JSON from stdin (up to 64 MiB) and fills `session`, `event`, `tool`, `cwd`, `toolInput`, `conversation`, `prompt` (clipped to 4 KiB), `source`, background fields and `detail`.

## Behaviour

- Wrong method: 405. Bad JSON: 400. Token mismatch: 403 (constant-time compare).
- If `session` is non-empty the event is passed to the application's callback.
- On `SessionStart` the context handler's text is returned as `context` (the pane briefing).
- On `UserPromptSubmit` and `PostToolUse` the notice handler's text is returned as `context` if there is mail to announce ([notices not typed](/gotchas/notices-not-typed.md)).
- On `PreToolUse` the review handler may answer allow ([auto-review](/security/auto-review-read-only.md)).
- Nothing to say: 204. Otherwise 200 with JSON `{"context", "permissionDecision", "permissionReason"}`; the decision is only ever `"allow"`, there is no deny.

## Client side filtering

`Emit` drops `Notification` events whose type is `auth_success`, `elicitation_complete`, `elicitation_response`, `agent_completed` or `computer_use_exit` (news of something finished, which must not turn the pane amber). A `PostToolUseFailure` with `is_interrupt` is renamed `Interrupted`.
