---
type: Package
title: chat streaming parsers
description: The three vendor wires in the chat client, how each reads a streamed answer, and what they share.
resource: internal/chat/wire.go
tags: [chat, streaming, anthropic, openai, gemini, sse]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/wire.go, internal/chat/anthropic.go, internal/chat/openai.go, internal/chat/gemini.go, internal/chat/session.go]
---
# Chat streaming parsers

Part of [chat](/packages/chat.md). `NewWire(name, baseURL, key)` returns one of three `Wire` types; each has `Stream(ctx, Request, emit)` that posts a JSON body and turns the server-sent events into `Event`s: text, thinking, call, reasoning, usage, notice. Addresses are built as in [chat endpoint addresses](/gotchas/chat-endpoint-addresses.md).

## Shared

- `post` sends `Content-Type: application/json` and `Accept: text/event-stream`. Status 300 or more becomes an `apiError` with the body's message (first 8 KiB) and a `Retry-After` read as seconds or an HTTP date.
- `httpClient` has no timeout; the context ends a turn. Redirects are followed only to the same host and never from https to http (the key is in a header).
- `readSSE` joins several `data:` lines with newlines, ignores `:` comments, and `readLine` fails on a line over 8 MiB (`sseMaxLine`).
- `callBuffer` collects tool-call arguments in pieces. Arguments that are empty or not valid JSON become `{}` with the text kept in `BadArgs`.
- An error chunk inside a stream (`streamFailure`) is mapped by `streamErrorCodes` (for example `overloaded_error` to 529, `rate_limit_error` and `RESOURCE_EXHAUSTED` to 429) to the same `apiError` a status would give, so the loop can retry.
- Every wire emits `EventUsage` last, also when the stream failed or was cut off.

## Anthropic

Header `anthropic-version: 2023-06-01` and `x-api-key`. A model is required. `max_tokens` defaults to 32000 (`defaultMaxTokens`); if the API refuses with "max_tokens: N > M" and the user set no ceiling, the request is repeated with M. `cache_control: ephemeral` is set on the system prompt and on the last block of the last message. Events read: `content_block_start/delta` (text, thinking, signature, partial JSON), `message_delta` (stop reason, running usage totals, which replace rather than add), `message_stop`. Stop reasons `max_tokens`, `refusal` and `model_context_window_exceeded`, or no `message_stop`, become errors. Signed or redacted thinking blocks are returned with the next request. Usage `In` includes cache reads and writes.

## OpenAI

Covers every OpenAI-compatible server. Bearer auth, `stream_options.include_usage`, `max_completion_tokens` only when a ceiling was set. If the server refuses over `stream_options`, it retries without; if it says the model "does not support tools", it retries without tools, sets `noTools` and emits a notice. Reasoning is read from `reasoning_content` or `reasoning`. `[DONE]` ends the stream. Finish reason `length` is a cut-off, `content_filter` a refusal; neither a reason nor `[DONE]` is a dropped connection. Tool-call fragments are matched by index, and a new id at a known index starts a new call. Cached tokens and reasoning tokens go into `Usage`.

## Gemini

Path `/models/<model>:streamGenerateContent?alt=sse` (a `models/` prefix is stripped), header `x-goog-api-key`. A model is required. The system prompt is `systemInstruction`; the assistant role is `model`; tool answers go in a user entry as `functionResponse`, paired by tool name because a call has no id. Thought parts become thinking events, and `thoughtSignature` rides on the call. A `promptFeedback.blockReason` or finish reasons such as SAFETY and RECITATION are refusals, `MAX_TOKENS` a cut-off, no finish reason a dropped connection, anything but STOP an error.
