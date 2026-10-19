---
type: Environment Variable
title: FLOCKDECK_API_KEY
description: Fallback API key used by any agent that has no key of its own.
resource: internal/agent/available.go
tags: [secrets, keys, env]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/available.go]
---
- **Default:** unset.
- **Name constant:** `agent.FallbackKeyEnv`.
- **Read by:** `creds.Resolve` through `agent.KeyNames`, as the last source after the agent's own variables and its stored key. Whitespace is trimmed; an empty value counts as unset.
- **Effect:** supplies the key for any API agent whose own variables and [keys.json](/data/keys-file.md) entry are empty. If the spec already lists it among its own names it is not repeated at the end. Specs with no key variables and no base URL get no env names at all.
- Not used for the TypeSafe key, which is stored separately.

See [API key handling](/security/api-key-handling.md).
