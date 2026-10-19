---
type: Security Boundary
title: API key handling
description: How stored and environment API keys are resolved, written and kept out of logs and UI.
resource: internal/creds
tags: [security, secrets, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/creds/creds.go, internal/creds/store.go, internal/creds/jevkey.go, internal/agent/available.go, internal/testiso/testiso.go]
---
Mechanism in [creds](/packages/creds.md):

- **Opaque type.** `creds.Key` keeps its value in an unexported field. `String()` and `GoString()` print only how the key was found, and `encoding/json` sees no field, so formatting or marshalling a `Key` cannot leak it. `Secret()` is the only accessor.
- **Status, not value.** The UI, CLI and snapshots receive `creds.Status` (agent, set, source, variable name). The value reaches one place: the environment of the `flockdeck chat` process for the pane that needs it, via `creds.Env`.
- **Resolution order.** The spec's own env variables, then the store entry for the agent id, then `FLOCKDECK_API_KEY`. Vendor variables (for example a vendor's usual key variable) are consulted only when the entry talks to that vendor's own host (`agent.VendorsOwn`), so a key exported for the vendor is not sent to a user-set gateway.
- **File.** [keys.json](/data/keys-file.md) inside the state directory (which is 0700), file mode 0600, written to a temp file then renamed. Encode errors are replaced with a fixed message because the JSON error would carry the value.
- **Reserved ids.** Ids starting with `@` (the TypeSafe key, `@typesafe`) are refused by `Set`/`Clear` and hidden from `Names`, so the agent-keys dialog and `flockdeck keys` neither show nor change it. Only `JevKey()` returns that value, for the client that sends it to the TypeSafe API.
- **Test isolation.** [testiso](/packages/testiso.md) unsets `*_API_KEY`, `*_API_TOKEN` and `*_AUTH_TOKEN` variables and redirects the config dir during tests.

Related: [state directory permissions](/security/state-dir-permissions.md), [FLOCKDECK_API_KEY](/config/flockdeck-api-key.md).
