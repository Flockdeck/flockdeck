---
type: Gotcha
title: Chat endpoint addresses
description: How the chat client joins a configured base address with each API's path, and how it picks up a changed address.
resource: internal/chat/wire.go
tags: [chat, endpoint, base-url, gateway]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/wire.go, internal/chat/endpoint.go]
---
# Chat endpoint addresses

A base URL for a chat agent can be a vendor root, a gateway path or a copied request URL. `joinEndpoint` handles all three (see [chat streaming parsers](/packages/chat-streaming-parsers.md)).

- An empty base means the vendor's own host: `https://api.anthropic.com`, `https://api.openai.com`, `https://generativelanguage.googleapis.com`.
- The base is parsed as a URL. Its query is kept (Azure's `api-version`, for example) and the API path is added to the path, not the query. A fragment is dropped. A base that does not parse is pasted onto as it stands.
- A trailing `/chat/completions`, `/messages` or `/models` is removed first, so pasting a full request URL works.
- Anthropic and Gemini (`endpoint`): the version segment (`v1` for Anthropic, `v1beta` for Gemini) is added unless the base's last path segment already matches `v<digits>` with optional `alpha` or `beta` and a number.
- OpenAI-compatible (`openaiEndpoint`): `/v1` is added only when the base has no path at all. A base with any path is taken as the API root already.

## Address changed while a pane runs

`EndpointStore` is a hook the application sets to return the agent's current base URL. When a request cannot reach the address, `readdress` asks it again; if the value differs from the one the chat started with, it builds a new wire and reports true, so changing the address with `flockdeck keys endpoint` and then using `/retry` works without restarting the pane. It does nothing when a wire was injected in `Options`, when no store is set, or when no agent id is known. See [flockdeck keys](/cli/keys.md).
