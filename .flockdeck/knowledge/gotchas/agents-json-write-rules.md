---
type: Gotcha
title: agents.json read and write rules
description: Why agents.json is read raw, locked, atomically replaced, and never overwritten when unreadable.
resource: internal/agent/config.go
tags: [agents-json, windows, atomic-write]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/config.go]
---
Things the code in `internal/agent/config.go` does on purpose:

- **Raw entries.** Agents and roles stay as `json.RawMessage` so a field the user set to `""` or `[]` is told apart from one they left out. Lists of structs start empty before the entry is decoded onto the built-in, because the decoder reuses slice elements and an entry's `models` came out with the built-in's names.
- **Process-wide lock.** Reads and writes share one mutex. Twenty concurrent default saves once lost nineteen and failed eleven with "Access is denied" on Windows, because Windows refuses to rename over a file that another save or the picker has open.
- **Atomic write with retry.** `store.WriteAtomic` (temp file, flush, rename, mode 0600) waits out a short-lived open handle from a virus scanner or another instance.
- **Unreadable file is never overwritten.** `SetDefaults` and `SetRouting` return the read error instead, since the file may be the only copy of the user's agents.
- **BOM and empty file.** A UTF-8 BOM (Notepad, Windows PowerShell) is stripped. A file with only white space counts as absent.
- **No HTML escaping.** `&`, `<` and `>` are written as typed (an install line with `&&`).
- **Unknown top-level keys survive** a save (`File.Extra`).
- **Multiple entries for one agent.** Later entries merge over earlier ones.
- **Built-in lists are cloned** before merging, because a bad entry once wrote its lists into the built-in's own (Claude stopped stripping `CLAUDECODE`).
- **Ids are exact.** `Claude` is a new agent, not the built-in, and the notice says so.

See [agents.json](/data/agents-json.md) and [atomic state writes](/gotchas/atomic-state-writes-and-windows-contention.md).
