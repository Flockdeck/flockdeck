---
type: Package
title: record
description: Writes a pane's agent conversation to disk as a redacted, size-capped JSON Lines transcript, and exports stored conversations the same way.
resource: internal/record/record.go
tags: [recording, redaction, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/record/record.go, internal/record/redact.go, internal/record/export.go, internal/record/rpc.go]
---
Off for every pane until the user turns it on. Never writes into a project repository. Input is the agent's stored conversation (via `session/transcript` followers and exporters), not the terminal screen.

## API

| Symbol | Role |
|---|---|
| `Manager` (`NewManager(dir func())`) | Owns open files, one per conversation: `Write`, `Finish`, `Active`, `Path`, `Close`, `SetMaxFileBytes` |
| `Sync(m, meta, follower)` | The one path that makes a transcript; both recording and export call it |
| `Export`, `FindExport`, `CheckExportPath`, `InRecordings`, `MetaFor` | Export a stored conversation |
| `List`, `Root`, `EnsureRoot`, `Folder`, `SessionID` | Locate files |
| `Redact`, `Find`, `FindTokens`, `Clip`, `RedactValue`, `ClipValue`, `SecretName`, `KeyIsSecret`, `Kinds` | Redaction, see [recording redaction](/security/recording-redaction.md) |
| `schemacheck` subpackage | Validates JSON against the small JSON Schema subset used for the line schema, for tests |

Format: [transcript line](/data/recording-line.md). Consumers: [workspace](/packages/workspace.md) (recording), [recordview](/packages/recordview.md) (reading).

## Disk layout

`<config dir>/recordings/<Folder>/<session>.jsonl`, folders 0700, files 0600. `Folder` is the slugified project name (max 40 chars) plus an FNV-1a hash of the lower-cased root. Exports go to `.../<Folder>/exports/` and delete nothing. A path-based export must not exist yet.

## Limits

- `MaxFileBytes` 16 MiB: the file ends with a `recording_truncated` line and `Write` returns `ErrFull`.
- `MaxFieldBytes` 8 KiB per string; `MaxMessageBytes` 32 KiB for prompts and assistant messages.
- Retention applied when a session starts in a folder: 30 days, then oldest first until at most 100 files and 256 MiB. The file just opened is never removed.

## Export safety

`CheckExportPath` refuses a path inside the conversation's project or inside any directory with a `.git` entry above it, following symlinks, because a transcript can hold secrets.

## Stream recordings

`Manager.StartRPC` opens a `<id>.rpc.jsonl` file beside a project's terminal recordings and returns an `RPCRecorder`. The structured-agent client is meant to call `Tap(dir, raw)` with each JSON-RPC line it reads or writes; it is not wired into `internal/acp` yet. Messages are redacted and clipped (`RedactValue`, 8 KiB a string, 32 KiB for prompt and message text) and `RPCHook` can clip or drop more. Neighbouring chunk updates of one kind, and updates of one tool call, are joined into one line and written within 250 ms. At `MaxRPCFileBytes` the file is rewritten without its oldest lines and a `rpc_dropped` line says how many. `RPCRecorder.Gap(n)` writes a `rpc_gap` line where `n` tapped lines were never recorded. The same `prune` retention covers these files. The end of each run of message text is held back until more arrives or the recording closes, and the last 16 KiB of written text is kept as context, so a secret split between chunks, or a value whose label is already written, is redacted; keys are redacted too; input over 1 MiB or 50,000 elements is a note, not parsed. `StartRPC` refuses once the Manager is closed, and retention spares recordings still open. `ReadRPCFile`/`ScanRPC` skip damaged lines, flag a torn tail and stop at 32 MiB. Format: `docs/rpc-recording-format.md` and `docs/rpc-recording-line.schema.json`.
