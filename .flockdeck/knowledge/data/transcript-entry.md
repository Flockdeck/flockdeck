---
type: Data Format
title: Conversation stream entry
description: The agent-neutral entry shape that transcript streams produce for a pane's chat view, with its kinds, caps and detail bodies.
resource: internal/session/transcript/stream.go
tags: [transcript, conversation, entry, json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/transcript/stream.go, internal/session/transcript/claude_stream.go, internal/session/transcript/chat_stream.go, internal/session/transcript/image.go, internal/server/conversation.go]
---
# Conversation stream entry

An `Entry` is one trimmed event of a conversation. Each adapter (`claudeStream`, `chatStream`) translates its own file format into entries; `StreamFor(spec, sessionID)` returns a `Stream` or false for an agent with no adapter. The server pages the entries to a client ([conversation commands](/api/ws-commands-conversation.md)). Entries are not stored on disk; they are rebuilt from the transcript ([session transcript readers](/packages/session-transcript.md)).

## Fields

JSON names and which kinds use them:

| Field | Kinds | Meaning |
|---|---|---|
| `id`, `kind`, `ts` | all | `ts` is RFC 3339 text |
| `text` | prompt, notice | |
| `markdown` | reply | |
| `chars`, `seconds` | thinking | Length in runes; the Claude adapter sets only `chars` |
| `name`, `label`, `status`, `summary`, `diff`, `hasDetail` | tool | `status` is `running`, `ok` or `error` |
| `description`, `report`, `status`, `hasDetail` | subagent | |
| `turns` | compaction | |
| `mediaType`, `bytes`, `width`, `height` | image | `hasDetail` is always true |
| `dir`, `peer`, `msg`, `delivery`, `text` | message | Mail between agents, structured panes only. `dir` is `in` or `out`, `delivery` one of `queued`, `delivered`, `handled`, `undeliverable`; changes in place |

Kinds: `prompt`, `reply`, `thinking`, `tool`, `subagent`, `compaction`, `notice`, `image`, `message`.

## Detail

`Stream.Detail(id)` returns the full body of a trimmed entry: `text` (full tool output, thinking text), `diff` (an oversized diff), `entries` (a subagent's own transcript), or `data` (an image as base64, never inline on the entry).

## Caps

| Constant | Value | Effect |
|---|---|---|
| `ToolOutputCap` | 4 KiB | One-line output over this, or any multi-line output, becomes a summary (`first line (N lines)`) with detail on demand |
| `DiffCap` | 64 KiB | A larger diff becomes `+A -D lines changed` with the diff as detail |
| subagent description / report | 200 / 4000 | Truncation in the Claude adapter |

## Adapter behaviour

- Claude: a tool call becomes a `running` entry that is updated in place when its result arrives. Labels are `Edited x`, `Wrote x`, `Read x`, `Ran <first line, 60 chars>`, `Searched for <pattern>`. Edit, MultiEdit and Write calls carry a unified diff built from the call's input. The tools `Agent` and `Task` become subagent entries. User text is classified by `noticeRules`: system reminders, hook output and caveats are dropped; slash-command lines are shown as is; task notifications, cross-session messages, system notifications and a compaction resume note become one-line notices with the original as detail. A line marked human by Claude Code is always a prompt. `Reset()` is always false because a Claude `/clear` changes the session id.
- Chat: only prompt, reply and tool lines produce entries. A tool counts as `error` when its text starts with one of six fixed phrases such as "the tool failed:" or "not run:". A `clear` line empties the stream and makes `Reset()` return true once.
- Light mode (`SetLight(true)`) returns new entries from `Refresh` but retains none.
- Images: only png, jpeg, gif and webp are accepted; others (svg above all) are refused so a crafted transcript cannot inject markup.
