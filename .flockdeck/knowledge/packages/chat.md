---
type: Package
title: chat
description: Flockdeck's own terminal chat client that talks straight to a model API and runs as an agent pane.
resource: internal/chat
tags: [chat, api-runner, anthropic, openai, gemini]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat]
---
# internal/chat

The program behind `flockdeck chat`, which is what an API-runner agent from [agent](/packages/agent.md) starts in a pane. It streams a conversation with a model, asks before running tools, keeps a transcript, and reports its lifecycle and spending to the app.

## Main pieces

| Piece | Notes |
|---|---|
| `ParseArgs`, `Options`, `Run(ctx, Options)` | Flags `-agent -model -session -resume -wire -base-url -key-env -max-tokens -cwd`, remaining args are the opening task. Env fallbacks: [chat flag env](/config/chat-flag-env.md) |
| `Wire`, `NewWire(name, baseURL, key)` | Three request shapes: `anthropic` (default), `openai` (also `openai-compatible`), `gemini` (also `google`). Default hosts are the vendors' own; `baseURL` overrides |
| `Tool`, `ToolCall`, `AlwaysApprover` | Tools are supplied from outside; the loop handles approval, events and feeding results back |
| `Log`, `Entry`, `ReadEntries`, `Conversations` | Transcript: [chat transcript](/data/chat-transcript.md) |
| `KeyFor`, `KeyStore`, `EndpointStore` | Key lookup and re-lookup hooks set by the application |
| `CheckKey` | Probe a key against the endpoint |
| `reporter` | Sends `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Notification`, `Stop`, `SessionEnd` through [hooks](/packages/hooks.md) `Emit`, and per-call usage through `Report`; so a chat pane shows status like a Claude pane |

## Slash commands

`/help`, `/model`, `/output [n]`, `/history [n]`, `/retry`, `/clear`, `/status`, `/forget`, `/exit` (alias `/quit`). A line ending in a backslash continues on the next line.

## Tool approval

A tool question is also sent as a `Notification` event (the pane turns amber). Answers: `y`/yes/ok, `a`/always (per tool "always key", session only; `/forget` withdraws), Enter or `n` declines, `stop`/`cancel`/`abort` ends the turn; any other text declines and is passed to the model as what to do instead.

## Key handling

See [chat API key handling](/security/chat-api-key-handling.md). With no key and a terminal attached, the client waits for one to be stored instead of exiting.

## Environment read

`NO_COLOR`, `TERM=dumb` (disable colour), `FLOCKDECK_COLUMNS` then terminal width then `COLUMNS` for width ([env](/config/flockdeck-columns.md)). Colour is also off when stdout is not a console.
