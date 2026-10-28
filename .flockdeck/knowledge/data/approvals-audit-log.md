---
type: File Format
title: remote-approvals.log
description: Append-only JSON-lines audit log of phone approval connections, cards shown, decisions, refusals and desk changes.
resource: internal/server/approvals_socket.go
tags: [approvals, audit, state]
timestamp: 2026-10-28T00:00:00Z
code_paths: [internal/server/approvals_socket.go, internal/server/artifacts_audit.go, internal/server/approvals_prefs.go]
---
Stored as `remote-approvals.log` in the state directory, rotated at 1 MiB like [remote-artifacts.log](artifacts-audit-log.md), with the same `auditEvent` line and the same cleaning of text fields.

| `event` | When | Notable fields |
|---|---|---|
| `connect` | a socket opened and passed every check | `device`, `deviceName` |
| `denied` | a socket or device refused, with `reason` (`disabled`, `verify`, `key`, `origin`, `grant`, `handshake`, `connect-rate`, `busy`, `rate`, `malformed`, `refused-answers`); recorded at most every 10 s per device and reason, `suppressed` counts the rest | `reason` |
| `shown` | a card was sent to a socket | `pane`, `name`, `seq`, `kind`, `len`, `hash`, `reason` (`desk-only` or `deny-only` when nothing or only Deny is offered) |
| `decision` | a decision was accepted, written before the answer is given | the same, and `decision` (`allow_once` or `reject_once`) |
| `decision-refused` | a decision was refused | `reason` (`rate`, `shape`, `decision`, `offer`, `ended`, `mismatch`, `allow-not-offered`, `too-soon`, `stale`, `restarted`, `no-card`, `changed`, `audit`, `error`) |
| `device-approve-on`, `device-approve-off`, `approvals-off` | the desk changed approval (`approvals-off` is the kill switch; `len` is how many devices it was on for) | `reason` `not-saved` when it could not be saved |

`hash` is the SHA-256 of the text the phone was shown and `len` its length in bytes. The text, the command and the path are never written.
