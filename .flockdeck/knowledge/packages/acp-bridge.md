---
type: Package
title: acp bridge
description: Turns the ACP update stream into transcript entries, with throttled hand-out and bounded memory.
resource: internal/acp/bridge/bridge.go
tags: [acp, agents, transcript, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/acp/bridge/bridge.go, internal/acp/bridge/sanitize.go, internal/session/transcript/summary.go]
---
- `New(Config)`; `Handler(answer)` returns an [acp](acp.md) `Handler` that feeds `Update` and wraps each permission request in `PermissionOpen`/`PermissionClosed`. The caller brackets a turn with `TurnStart(text)` and `TurnEnd(stopReason)`.
- Entries are `transcript.Entry` values in arrival order. Consecutive message or thought chunks merge into one entry; a tool call is one entry, updated in place by `toolCallId`. An update for a tool never announced becomes a tool entry. Unknown kinds and chunks without content are ignored; an unknown content block becomes `[type]`.
- `Flush()` hands out what is due: new entries and status changes at once, entries whose content only grew once per `MinInterval` (250ms). Ten updates inside the interval are one hand-out of the latest state. `Snapshot()` returns everything as it stands.
- A pending permission wins over `in_progress` updates for `Status()` (waiting) and `Activity()`. A tool refused and then `end_turn` leaves no notice; `cancelled`, `max_tokens` and `refusal` do.
- Long tool output and large diffs show a summary (`transcript.SummarizeToolOutput`, `DiffSummary`) with the full text kept for `Detail(id)`. Kept detail is bounded by `DetailBudget` (64 MiB): the oldest entry holding detail loses it first, wherever in the list an update arrives, and its entry loses `HasDetail`. An update that repeats the same output is not summarized again.
- The agent is hostile. Every string is cleaned (`clean`: ESC/CSI/OSC sequences, C0 except newline and tab, CR, DEL, C1, line separators, bidi marks; bad UTF-8 becomes U+FFFD) and capped: one-line fields (labels, names, ids, card fields) at 200 runes, a command at 4096, a path at 1024, a card at 16 options, a plan at 100 items. A reply or thought stops growing at 4 MiB. The card shows cleaned option ids; `Handler` answers with the agent's own.
- Entries are capped (`MaxEntries`, 10000) and so is inline text (`TextBudget`, 64 MiB): past either the oldest entries other than prompts are dropped, down to 90%, and counted by `Dropped`. Tool ids are keyed by a hash of the whole id; a second `tool_call` for a known id is ignored; updates with no id each get their own entry.
- `Flush` hands out at most 64 new or status-changed entries per `MinInterval`, and an entry's status changes go out at once only four times a second.
- Tests: `bridge_test.go` uses a fake clock; `limits_test.go` covers the budgets, caps, rate limits and sanitizing; `e2e_test.go` runs the fake agent through the real client (plain turn, tool in place, permission while chunks stream, cancel, and the `storm` flood with a heap bound).
