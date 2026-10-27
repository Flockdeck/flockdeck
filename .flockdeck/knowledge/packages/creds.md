---
type: Package
title: creds
description: Resolves the API key an agent needs and keeps it out of logs, JSON and the UI.
resource: internal/creds
tags: [security, secrets, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/creds/creds.go, internal/creds/jevkey.go, internal/creds/store.go]
---
Finds the API key for an agent `Spec` and stores keys the user saves. The value is read by `flockdeck keys` and by the baton scrubber, which removes known keys from handoff text. `Env` builds the entry a pane would receive, but nothing calls it since the API runners were removed; board task T202 hands a stored key to an agent's adapter instead.

## Main API

| Name | Role |
|---|---|
| `Resolve(spec)` | Returns a `Key`. Order: the Spec's own env variables (from `agent.KeyNames`), then the store entry under the Spec id, then `FLOCKDECK_API_KEY`. |
| `Key` | Unexported `secret`; `Secret()` is the only way to the value. `String` gives "set (ENV_NAME)" or "set (store)"; `GoString` is redacted too. |
| `Env(spec)` | Env entries for a pane: only for a stored key, under the first own variable name. A key already in the environment is inherited, not re-exported. No caller until T202. |
| `Status` | JSON-safe view: agent, name, set, source ("env" or "store"), env variable name. |
| `Set`, `Clear`, `Has`, `Names` | Manage the store. `Set` trims, rejects empty keys and ids beginning with "@". |
| `JevKey`, `SetJevKey`, `ClearJevKey`, `HasJevKey` | The TypeSafe key, stored under the reserved id `@typesafe`; hidden from `Names`. |

## Disk

Store file is [keys.json](/data/keys-file.md) in the state dir, mode 0600, written via temp file + `store.RenameWithRetry`. A package-level mutex serialises read-modify-write (twenty parallel sets once kept one key on Windows). Reads go through `store.ReadState`.

Used by the root `flockdeck` program (`keys`), `internal/server`, `internal/session` (status assist) and `internal/workspace` (baton scrubbing). The key check and the lookup that `keys check` uses are in [apikey](/packages/apikey.md).

See [API key handling](/security/api-key-handling.md) and [FLOCKDECK_API_KEY](/config/flockdeck-api-key.md).
