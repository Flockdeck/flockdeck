---
type: Environment Variable
title: FLOCKDECK_AGENT, _MODEL, _WIRE, _BASE_URL, _KEY_ENV
description: Environment stand-ins for the flockdeck chat flags of the same names.
resource: internal/chat/flags.go
tags: [chat, pane-env]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/flags.go, internal/legacyenv/legacyenv.go]
---
Each variable is read only when the matching flag was not given, through `legacyenv.GetWarn`, which prefers `FLOCKDECK_<NAME>` and falls back to `PERCH_<NAME>` (a warning is printed once per name, naming the removal date).

| Variable | Flag | Effect |
|---|---|---|
| `FLOCKDECK_AGENT` | `-agent` | Catalog id the pane was started as; names the key to look for |
| `FLOCKDECK_MODEL` | `-model` | Model to ask for |
| `FLOCKDECK_WIRE` | `-wire` | `anthropic`, `openai` or `gemini` |
| `FLOCKDECK_BASE_URL` | `-base-url` | Endpoint root; empty means the catalog entry, then the vendor's own |
| `FLOCKDECK_KEY_ENV` | `-key-env` | Comma-separated names an API key may arrive in |

The same lookup reads `FLOCKDECK_API` and `FLOCKDECK_TOKEN` (the pane's callback address and token, [token env](/config/flockdeck-token.md)) and `FLOCKDECK_PANE` (the session id when none was named). A pane's argv built from an [agent spec](/data/agent-spec.md) can state wire, base URL and key names as literals, or put them in `Spec.Env`; neither is the only way.
