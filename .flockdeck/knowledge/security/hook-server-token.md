---
type: Security Boundary
title: Hook server loopback and run token
description: How the pane callback server limits who can post events and commands, and what it does not prove.
resource: internal/hooks/hooks.go
tags: [security, loopback, token]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# Hook server access

- **Bind address:** `127.0.0.1` on a random port (`net.Listen("tcp", "127.0.0.1:0")`). Reachable by any local process, so the token is required.
- **Token:** 16 random bytes from `crypto/rand`, hex encoded, made once per run. Every route compares it with `subtle.ConstantTimeCompare` and answers 403 on mismatch. Panes receive it in their environment ([FLOCKDECK_TOKEN](/config/flockdeck-token.md)).
- **Body limit:** 1 MiB per request; `ReadHeaderTimeout` 5 s.
- **What the token proves:** that the caller is a process Flockdeck started (or something with that process's environment). The token is the same for every pane of a run, so it does not identify which pane. The pane id in a request is only what the caller says; the application refuses a pane it does not have open, and the pane acts as itself. The tasks and team code state that a permission here keeps agents to their jobs and is no defence against an agent set on getting round it ([task permissions](/security/task-board-permissions.md)).
- **No deny:** the only permission answer `/hook` can give is `allow` ([auto-review](/security/auto-review-read-only.md)).
- **Failure mode:** a refused hook is reported on stderr by `Emit`; a hook failing never blocks the session it reports on.

The server is separate from the web/HTTP server and the [local channel](/security/local-channel-access.md); see also [local token and window link](/security/local-token-and-window-link.md).
