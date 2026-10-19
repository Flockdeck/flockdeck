---
type: File Format
title: Chat transcript
description: JSON-lines file where flockdeck chat records each conversation, one file per session.
resource: internal/chat/transcript.go
tags: [chat, transcript, jsonl]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/transcript.go]
---
# Chat transcript

`<state dir>/chats/<session>.jsonl` (the folder is created mode 0700). The session id is the pane id. Each entry is written through immediately, not buffered, because other code reads the file while the pane runs. An empty file counts as no transcript (`Path` returns "").

## Entry (one JSON object per line)

| Field | Meaning |
|---|---|
| `type` | `user`, `assistant`, `tool`, or `clear` (written by `/clear`) |
| `ts` | RFC 3339 with nanoseconds on write. On read also accepts other layouts or a number (seconds, or milliseconds when above 1e11) |
| `text` | Message or tool output |
| `tool`, `call` | For `tool` entries: tool name and the call as drawn, such as a read of a path |
| `cwd`, `agent`, `model` | Directory, catalog id of the agent, model that wrote an answer. `agent` matters because every API agent shares this folder |
| `in`, `out` | Token counts |

Readers: `ReadEntries`, `Messages` (rebuilds the conversation to resume), `Replies`, `Conversations(cwd)` (history picker), plus the `/history` command and the replay shown on resume. Lines longer than a bound are skipped rather than failing the read. See [chat](/packages/chat.md).
