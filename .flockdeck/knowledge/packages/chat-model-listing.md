---
type: Package
title: chat model listing and /model
description: How the chat client asks an endpoint which models it has and how /model lists, numbers and matches them.
resource: internal/chat/modelchoice.go
tags: [chat, models, slash-command]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/modelchoice.go, internal/chat/models.go, internal/chat/session.go, internal/chat/keys.go]
---
# Chat model listing

Part of [chat](/packages/chat.md). The catalog entry for an agent may list models as `ModelChoice` (`ID`, `Name`, `Note`). When it lists none, the endpoint is asked.

## Asking the endpoint

Each wire implements `ListModels(ctx)` (the `modelLister` interface) with a GET through `getJSON`, which reads at most 4 MiB and returns an `apiError` for status 300 or more.

| Wire | Request | Result |
|---|---|---|
| openai | `<base>/models` (version added as for any OpenAI-style base) | `data[].id`, sorted |
| anthropic | `/v1/models?limit=100` | `data[].id` in the order sent (newest first) |
| gemini | `/v1beta/models?pageSize=200` | `models[].name` without `models/`, only those supporting `generateContent`, sorted |

## /model

- With no argument: prints the model answering, asks the endpoint if the catalog has none (10 second limit), then lists up to 40 (`maxListedModels`) numbered, marking the current one with `*`. The current model is always shown even beyond 40; the rest are counted as hidden.
- A number picks from the list; out of range is an error line.
- Any other text goes through `matchModel`, case-insensitive: exact id wins; then exact name; then a single model whose id or name contains the text. No match means the text is used as the id, so a model the list omits can still be named. Several matches switch nothing and list the matches (up to 40) with their numbers.
- If nothing is listed yet, the endpoint is asked before matching.

## Without a named model

`pickOnlyModel` runs for a chat started with no model. It asks only when the catalog lists none and `isLoopback(BaseURL)` is true, with a 3 second limit, so no request goes to a vendor before the user asks anything. If the server offers exactly one, it is used. `localModels` returns the ids for a loopback endpoint only when there are 8 or fewer, for use in messages.

An unreachable endpoint gives a dim notice: for loopback it asks whether the model server is running; otherwise it suggests the connection or address (see [chat endpoint addresses](/gotchas/chat-endpoint-addresses.md)). Other errors are shown with their text.
