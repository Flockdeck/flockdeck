---
type: File Format
title: remote-approvals.log
description: Append-only JSON-lines audit log of phone approval connections, cards shown, decisions, refusals and desk changes.
resource: internal/server/approvals_socket.go
tags: [approvals, audit, state]
timestamp: 2026-10-28T00:00:00Z
code_paths: [internal/server/approvals_socket.go, internal/server/artifacts_audit.go, internal/server/approvals_prefs.go]
---
Stored as `remote-approvals.log` in the state directory (the `shown` and `denied` lines are in `remote-approvals-shown.log` and `remote-approvals-denied.log`, so that neither can rotate decisions out), rotated at 1 MiB like [remote-artifacts.log](artifacts-audit-log.md), with the same `auditEvent` line and the same cleaning of text fields.

| `event` | When | Notable fields |
|---|---|---|
| `connect` | a socket opened, passed every check and had its first request opened | `device`, `deviceName` |
| `denied` | in `remote-approvals-denied.log`, not this file. A socket or device refused, with `reason` (`disabled`, `verify`, `key`, `origin`, `grant`, `handshake`, `connect-rate`, `busy`, `rate`, `malformed`, `padding`, `unpadded`, `refused-answers`); at most every 10 s per device and reason and 30 a minute in all, `suppressed` counts the rest | `reason` |
| `shown` | in `remote-approvals-shown.log`. A card the phone can answer was sent to a socket, once per card per socket, at most 20 a minute per device and 60 in all (`suppressed` counts the rest) | `pane`, `name`, `seq`, `kind`, `len`, `hash`, `reason` (`deny-only` when only Deny is offered) |
| `decision` | a decision was accepted, written and flushed to disk before the answer is given; if it cannot be, nothing is given | the same, and `decision` (`allow_once` or `reject_once`) |
| `decision-refused` | a decision was refused; at most every 10 s per device and reason, except one that follows a `decision` line, which is always written | `reason` (`rate`, `shape`, `decision`, `offer`, `ended`, `mismatch`, `allow-not-offered`, `too-soon`, `stale`, `restarted`, `no-card`, `changed`, `audit`, `error`, or the gate's word if approval was switched off mid-decision) |
| `device-approve-on`, `device-approve-off`, `approvals-off` | the desk changed approval (`approvals-off` is the kill switch; `len` is how many devices it was on for) | `reason` `not-saved` when it could not be saved |

`hash` is the SHA-256 of the text the phone was shown and `len` its length in bytes. The text, the command and the path are never written.

The desk's own lines (`device-approve-on`, `device-approve-off`, `approvals-off`) are written off the workspace goroutine, because the log's lock is held while a decision line is flushed and a slow disk must not stall the workspace.
