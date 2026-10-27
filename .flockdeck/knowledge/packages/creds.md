---
type: Package
title: creds
description: Resolves the API key an agent needs and keeps it out of logs, JSON and the UI.
resource: internal/creds
tags: [security, secrets, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/creds/creds.go, internal/creds/jevkey.go, internal/creds/store.go]
---
Finds the API key for an agent `Spec` and stores keys the user saves. The value is read by `flockdeck keys` and by the baton scrubber, which removes known keys from handoff text. `StoredKey` is the key the workspace hands to `structured.Options.Key`, which sets it in the adapter's environment under `Spec.ACPKeyEnv` and nowhere else (T202).

## Main API

| Name | Role |
|---|---|
| `Resolve(spec)` | Returns a `Key`. Order: the Spec's own env variables (from `agent.KeyNames`), then the store entry under the Spec id, then `FLOCKDECK_API_KEY`. |
| `Key` | Unexported `secret`; `Secret()` is the only way to the value. `String` gives "set (ENV_NAME)" or "set (store)"; `GoString` is redacted too. |
| `StoredKey(spec)` | The stored key for a structured agent that has a key variable; "" otherwise, for a key from the environment, for a control character in it, or over 4 KiB. |
| `Env(spec)` | `StoredKey` as a `NAME=value` entry under `Spec.ACPKeyEnv`. |
| `Status` | JSON-safe view: agent, name, set, source ("env" or "store"), env variable name. |
| `Set`, `Clear`, `Has`, `Names` | Manage the store. `Set` trims, rejects empty keys and ids beginning with "@". |
| `JevKey`, `SetJevKey`, `ClearJevKey`, `HasJevKey` | The TypeSafe key, stored under the reserved id `@typesafe`; hidden from `Names`. |

## Disk

Store file is [keys.json](/data/keys-file.md) in the state dir, mode 0600, written via temp file + `store.RenameWithRetry`. A package-level mutex serialises read-modify-write (twenty parallel sets once kept one key on Windows). Reads go through `store.ReadState`.

Used by the root `flockdeck` program (`keys`), `internal/server`, `internal/session` (status assist) and `internal/workspace` (baton scrubbing). The key check and the lookup that `keys check` uses are in [apikey](/packages/apikey.md).

See [API key handling](/security/api-key-handling.md) and [FLOCKDECK_API_KEY](/config/flockdeck-api-key.md).
