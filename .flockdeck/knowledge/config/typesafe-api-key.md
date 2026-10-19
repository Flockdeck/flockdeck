---
type: Environment Variable
title: TYPESAFE_API_KEY
description: TypeSafe's own variable, used as the Jev API key when none is set in Settings.
resource: internal/jev/jev.go
tags: [jev, secrets, env]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jev/jev.go]
---
- **Default:** unset.
- **Name constant:** `jev.KeyEnv`.
- **Read by:** `jev.Key`, after the key stored in Settings; whitespace is trimmed.
- **Effect:** with a key, `jev.NewClient` succeeds; without one it returns `ErrNoKey` and callers quietly skip Jev. A key alone never sends anything: the callers check their own switches first.
- **Deliberately not used:** `FLOCKDECK_API_KEY` and the vendors' keys, so another vendor's key is never sent to TypeSafe.

See [Jev data handling](/security/jev-data-handling.md).
