---
type: HTTP Endpoint
title: POST /remote/approvals/stop
description: Switches phone approval off in memory and closes the approvals sockets after `flockdeck remote approvals off`.
resource: internal/server/approvals_prefs.go
tags: [http, remote, approvals, cli]
timestamp: 2026-10-28T00:00:00Z
code_paths: [internal/server/approvals_prefs.go, cli_remote_approvals.go]
---
Handler: `Server.handleApprovalsStop`.

- A request through the relay is refused with `403`. `POST` and URL token required, as for the artifacts stop.
- The CLI has already written the preferences file. Each device's record is the running program's to say, not the file's, so this switches `approve` off in memory too (`stopApprovals`), saves, ends the sockets of the devices it was on for and replies once that is done.
- With nothing on it still replies.

See [device access roles](/security/device-access-roles.md).
