---
type: Security Boundary
title: Jev status assist privacy limits
description: The only path that sends terminal output to a third party, gated by a user setting and a key, redacted and rate limited.
resource: internal/session/assist.go
tags: [security, privacy, jev]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/assist.go]
---
`StatusAssist` asks TypeSafe's Jev service to decide whether a quiet pane of an agent that reports no lifecycle is idle or waiting. The code names this the one place terminal content leaves the machine.

## Gates

- Off unless the user enabled the setting (`Enabled` is checked at every decision) and a TypeSafe key exists (settings or the `TYPESAFE_API_KEY` environment variable; see [api key handling](/security/api-key-handling.md)). A nil `*StatusAssist` asks nothing. Agents that report lifecycle hooks, and shells, never ask.
- Sent: only the last 30 lines, at most 2000 bytes, after stripping escape sequences and running `jev.Scrub` (pattern redaction from [record](/packages/record.md) plus secret-file-name words, 3+ consecutive base64 lines, joined wrapped tokens). Never scrollback, pane name or cwd. A tail that is only redacted text is not sent.
- Rate: one call per pane per 15 s, 12 per pane per hour, 10 per minute globally. Transient refusals pause all panes for 1 minute, refused requests for 15 minutes. One ask times out at 12 s.
- Effect is one-directional: it can move idle to waiting or blocked only, and only if the model's confidence and option probability clear fixed thresholds. Any failure leaves the heuristic status unchanged.
- Redaction is by pattern and best effort; other text in the last lines is sent as it appears.

See [session](/packages/session.md).
