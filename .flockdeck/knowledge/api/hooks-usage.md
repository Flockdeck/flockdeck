---
type: HTTP Endpoint
title: POST /usage
description: A pane reports what its agent has spent in tokens and cost, and subscription windows.
resource: internal/hooks/usage.go
tags: [hooks, usage, spend]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/usage.go, internal/spend/spend.go]
---
# POST /usage

Endpoint URL: `Server.UsageEndpoint()` (`<base>/usage`). Client: `hooks.Report(endpoint, token, report, timeout)`.

## Request

JSON `UsageReport`: a `spend.Report` plus `token`. Report fields: `session` (the pane id), `conversation`, `model`, `account`, `tokens`, `cost`, `cumulative` (totals replace rather than add), `windows` (subscription windows). Body at most 1 MiB.

## Response

405 wrong method, 400 bad JSON, 403 bad token, otherwise 204. The callback runs only if a handler is installed and `session` is non-empty.

## Notes

Separate from `/hook` because usage is not a lifecycle event and changes no pane status. The sender is the agent's own status line (Claude Code's, on each tick), so the client uses a caller-set timeout and a missing app only loses the figure. The package that interprets it is [spend](/packages/spend.md).
