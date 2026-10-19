---
type: Package
title: recordview
description: Reads a recording transcript file and serves bounded, cleaned, re-redacted pages of entries to an untrusted client.
resource: internal/recordview/recordview.go
tags: [recording, security, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/recordview/recordview.go]
---
`recordview.Open(ctx, root, name)` returns a `Reader`; `Reader.Page` returns a `Page` {`entries`, `next`, `done`, `skipped`}. `Header` carries session, conversation, agent, project and start time from the first line. No file bytes leave the package, only `Entry` values (an allowlist of fields).

## Guarantees

- Root must be an absolute plain folder; name is a short relative path (at most 8 components, 512 bytes). No `..`, drive/UNC/device paths, colons, backslashes, reserved device names. Every component is `Lstat`-checked and none may be a link of any kind. See `path.go`.
- Only version 2 transcripts are accepted (`ErrUnsupported` otherwise). File max is the writer cap plus 1 MiB.
- Text is cleaned (`clean.go`): invalid UTF-8, control chars except LF and tab, C1, whole terminal escape sequences, zero-width and bidi characters removed; then redacted with `record.Redact`.
- Bounds per call: line 256 KiB, scan 4 MiB, page 200 entries (default 50) and about 256 KiB, entry 160 KiB, tool input 64 KiB (replaced by `[input omitted: too large]`), time budget 500 ms.
- Cursor must follow a line break (`ErrBadCursor`). The reader holds no file handle between calls and returns `ErrChanged` if the file was replaced or shrank.
- Errors carry no paths or OS text.

The root and name must come from the host, never a client. Called by the server's recordings artifact handlers (`internal/server`). See [recording redaction](/security/recording-redaction.md), [transcript line](/data/recording-line.md).
