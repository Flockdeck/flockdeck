---
type: Package
title: apikey
description: Finds the API key stored or exported for an agent and asks the agent's endpoint whether it accepts it.
resource: internal/apikey
tags: [security, secrets, keys]
timestamp: 2026-10-27T00:00:00Z
code_paths: [internal/apikey/apikey.go, internal/apikey/check.go]
---
What was left of the removed `flockdeck chat` client after the chat client and the API runners went. It starts no pane. Board task T202 turns these keys into per-agent keys that are passed to adapters; until then `KeyStore`, `KeyFor` and `CheckKey` have no caller outside `flockdeck keys`.

## Main API

| Name | Role |
|---|---|
| `KeyStore` | Variable set by the caller (`cli_keys.go` sets it to a function over `creds`) that returns the stored key for an agent id. It is a variable so this package does not import `creds`. |
| `Target`, `KeyFor(target)` | Returns the key a pane for the target would send and where it comes from, in words that name the place and never the key. Order: the entry's own variables and the vendor's usual one, then the stored key, then `FLOCKDECK_API_KEY`. A loopback address with no key gives an empty key and the note "none, which a local endpoint does not need". |
| `CheckKey(ctx, wire, baseURL, key)` | Lists the endpoint's models, which every wire has and which costs nothing, and reports `nil`, `ErrKeyRefused`, `ErrUnreachable` or another error. Wires: `anthropic`, `openai` (also `openai-compatible`), `gemini` (also `google`). |

## Endpoint addresses

A base URL can be a vendor root, a gateway path or an address that already names the API version. `endpoint` adds `v1` (Anthropic) or `v1beta` (Gemini) only when the last path segment is not already a version; `openaiEndpoint` adds `/v1` only when the base has no path at all. A trailing request path such as `/models` is removed first.

See [API key handling](/security/api-key-handling.md), [creds](/packages/creds.md) and [flockdeck keys](/cli/keys.md).
