---
type: HTTP Endpoint
title: POST /team, /msg, /inbox
description: Roster, send-message and read-inbox routes behind flockdeck team, msg and inbox.
resource: internal/hooks/team.go
tags: [hooks, team, messaging, inbox]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/team.go]
---
# POST /team, /msg, /inbox

Three routes built from one generic handler, `serveJSON`: POST only, body at most 1 MiB, token compared in constant time, then the installed function. Handler errors are 400 with their own words; no handler is 503. Every request embeds `Auth{pane, token}`; the application refuses a pane it does not have open. Client calls time out after 10 s.

| Route | Request | Response |
|---|---|---|
| `/team` | `Auth` | `{members: [team.Member]}`, caller marked `self` |
| `/msg` | `Auth`, `target` (pane id, name or role), `text` | `{sent: [{id, pane, to, notifies}], failed: [..]}`. A role has one entry per live pane; `failed` names targets that did not get it |
| `/inbox` | `Auth`, `all`, `read` (message id) | `{messages: [team.Message]}`, oldest first. `read` returns that one message and marks it read; otherwise unread messages, or all with `all` |

`notifies` says the receiver's agent will be told in the reply to its next prompt or tool call; if false it finds mail only by running `flockdeck inbox`.

Semantics, limits and target resolution are in [team](/packages/team.md) and [team message](/data/team-message.md). The sender is whatever pane id the caller sent; see [hook server token](/security/hook-server-token.md).
