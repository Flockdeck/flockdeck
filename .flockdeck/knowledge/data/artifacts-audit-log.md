---
type: File Format
title: remote-artifacts.log
description: Append-only JSON-lines audit log of remote artifact connections, refusals and desk changes.
resource: internal/server/artifacts_audit.go
tags: [artifacts, audit, state]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/artifacts_audit.go]
---
Stored as `remote-artifacts.log` in the state directory, mode 0700 directory, owner-only file. Rotated at 1 MiB into `.1` and `.2` (three files in all).

One JSON object per line (`auditEvent`):

| Field | Meaning |
|---|---|
| `time` | UTC |
| `event` | for example `connect`, `open`, `stop`, `device-allow`, `device-revoke`, refusals |
| `device`, `deviceName` | relay device id and name |
| `kind`, `pane`, `name` | what was viewed; `name` is a path relative to the root |
| `bytes` | size |
| `reason` | refusal reason |
| `suppressed` | like refusals skipped since the last line written |

It records who, which kind and how many bytes; never content. Each text field is cleaned (`tidy`) and capped at 200 characters. Refusals are recorded at most every 10 s per device and reason. A connection is refused if its `connect` line cannot be written; the first page of a recording is not shown if its `open` line fails.
