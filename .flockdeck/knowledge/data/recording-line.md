---
type: File Format
title: Recording transcript line (.jsonl)
description: One JSON object per line, one file per recording session; format version 2.
resource: internal/record/record.go
tags: [recording, format]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/record/record.go]
---
Stored at `recordings/<project folder>/<session>.jsonl` under the config dir (see [record](/packages/record.md)). `<session>` is `YYYYMMDDTHHMMSSZ-<first 8 chars of conversation id>`.

## Fields (`record.Entry`)

`v` (2; raised when a field is removed, renamed or changes meaning, not when one is added; "pane" and "paneName" were removed in 2), `seq` (from 1, no gaps), `time`, `session`, `project`, `agent`, `model`, `conversation`, `gitBranch`, `cwd`, `agentVersion`, `type`, `text`, `usage`, `stopReason`, `title`, `trigger`, `tokensBefore`, `tokensAfter`, `tool`, `toolUseId`, `input`, `output`, `isError`, `interrupted`, `redacted`, `clipped`.

- `type`: `recording_started`, `recording_stopped`, `recording_truncated`, `user_prompt`, `assistant_message`, `tool_call`, `tool_result`, `conversation_title`, `conversation_compacted`.
- `usage`: `inputTokens`, `outputTokens`, `cacheCreationInputTokens`, `cacheReadInputTokens`; pointers, omitted when the source did not give a count. On the first line of a model reply only.
- `isError` and `interrupted` are written on every `tool_result`, true or false.
- `redacted`: something was replaced or a secret file's output was withheld.
- `clipped`: field name to original byte length for fields that were cut.
- The first line says "start of the transcript" and the last "end of the transcript", so a live recording and a later export are byte-identical.

[recordview](/packages/recordview.md) reads this format and accepts only version 2.
