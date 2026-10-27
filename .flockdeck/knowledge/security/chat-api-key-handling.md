---
type: Security Boundary
title: Chat client key and endpoint safety
description: Rules in the chat client and agent catalog that keep API keys from being sent to the wrong host or printed.
resource: internal/chat/keys.go
tags: [security, secrets, chat, api-keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/keys.go]
---
Direction (27 Oct 2026): `flockdeck chat` is planned for removal (T201); API keys stay in Settings and are passed to the agent's environment (T202). Status: planned removal. See docs/acp-direction.md.

# Chat key and endpoint safety

Complements [API key handling](/security/api-key-handling.md), which covers storage.

- **Lookup order** (`agent.KeyNames`, used by the chat client, the keys dialog and the picker): the entry's own variables (less the vendor's when it talks elsewhere), the vendor's usual variable only when the endpoint is the vendor's, then the key stored for the agent, then `FLOCKDECK_API_KEY`.
- **Vendor key stays with the vendor.** `agent.VendorsOwn` is true only for no `baseURL`, or an https URL on the vendor's host (`api.openai.com`, `generativelanguage.googleapis.com`, `api.anthropic.com`). Pointed anywhere else, `OPENAI_API_KEY` and similar are not sent.
- **Address with credentials refused.** `CheckBaseURL` rejects a URL with user info, without echoing it; only `http`/`https` with a host are accepted.
- **Redirects.** Wires follow a redirect only on the same host and never from https down to http (the key travels in `x-api-key` or `x-goog-api-key`, which Go does not strip). Stops after 10.
- **Redaction.** Vendor error text is passed through `redactKeys`, which replaces strings shaped like `sk-...` or `AIza...` with `[a key]`. No part of a key is printed. `/status` states where the key came from, never its value.
- **Rekey.** After a refusal the client retries with a different key (environment then stored); a refused key is not tried again.
- **Loopback endpoints** need no key; the check is case-insensitive on `localhost` and uses `IsLoopback` for IPs.
- **Pane command line.** An address is shown in the picker and sent on every pane's command line, which is why it may not carry a secret.
