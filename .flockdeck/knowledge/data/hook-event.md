---
type: Data Type
title: Hook event
description: The Event struct a pane posts to /hook for each agent lifecycle event.
resource: internal/hooks/hooks.go
tags: [hooks, event, lifecycle]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
# Event

JSON body of [POST /hook](/api/hooks-hook.md) (with `token` added). Event names are Claude Code's own; other agents, including [chat](/packages/chat.md), reuse them: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `StopFailure`, `Notification`, `PreCompact`, `PostCompact`, `SessionEnd`, and the synthetic `Interrupted`.

| JSON | Meaning |
|---|---|
| `session` | Pane id. Events with an empty value are ignored |
| `event` | Event name |
| `tool`, `cwd`, `prompt` | Tool name, directory, user prompt (clipped to 4 KiB) |
| `source` | `startup`, `resume`, `clear`, `compact`, `fork`; for compaction `manual` or `auto` |
| `conversation` | The agent's own conversation id; differs from `session` after `/clear` |
| `launch` | Value of `FLOCKDECK_LAUNCH`, new for each start of the pane process ([launch token](/gotchas/hook-launch-token.md)) |
| `toolInput` | Compact JSON of selected, size-capped parts of a `PreToolUse` input (each field at most 64 KiB). Opaque to everything between the hook and the phone-facing view |
| `notificationType` | Claude Code's `notification_type` (for example `idle_prompt` versus `permission_prompt`) |
| `background`, `backgroundId` | `start` or `end` of background work (a background Bash command or subagent) and its id |
| `backgroundTasks` | Whole list of background work still running at `Stop`; nil (absent) differs from an empty list |
| `backgroundInfo`, `backgroundTaskInfo` | Redacted, one-line, 200-character descriptions; at most 32 entries carried |
| `agentId` | Subagent that fired a tool event; empty for the main agent |
| `detail` | Data for the transcript recorder: tool use id, clipped input, result, error, last message, prompt. Built for every event of the relevant kinds, read only if the pane is recording |

Redaction of background text uses `record.Redact`.
