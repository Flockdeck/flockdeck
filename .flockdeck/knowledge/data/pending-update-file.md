---
type: File Format
title: pending.json (staged update)
description: Record of an update that was downloaded, verified and unpacked, waiting to be applied.
resource: internal/selfupdate/selfupdate.go
tags: [update]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/selfupdate/selfupdate.go]
---
Lives in `<state>/updates/` (`selfupdate.Dir`), beside the `staging` directory holding the unpacked binary.

| Field | Meaning |
|---|---|
| `version` | Release tag staged. |
| `binary` | Path of the staged program. |
| `chat` | Path of the staged Windows console twin (`flockdeck-chat.exe`), when the release has one. |
| `notes`, `url` | Release notes and page. |
| `staged` | UTC time staged. |
| `chosen` | Set by `StageChosen` (version picker): applied at next start without re-checking `Newer`. |

- Staging is atomic from the reader's side: a download goes to `staging.new-*`, is swapped into `staging`, then this record is saved. If saving fails the staging is discarded rather than left under the previous record's version.
- `Load` returns false if the record is absent or unusable; `Discard` removes record and staging; stale `staging.new-*` work dirs are swept once older than the request limit plus a minute.
- `Apply` removes it on success.

See [selfupdate](/packages/selfupdate.md).
