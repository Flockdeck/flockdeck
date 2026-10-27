---
type: Security Boundary
title: API key handling
description: How stored and environment API keys are resolved, checked, written and kept out of logs, the UI and the wrong host.
resource: internal/creds
tags: [security, secrets, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/creds/creds.go, internal/creds/store.go, internal/creds/jevkey.go, internal/apikey/apikey.go, internal/apikey/check.go, internal/agent/available.go, internal/testiso/testiso.go]
---
Mechanism in [creds](/packages/creds.md) and [apikey](/packages/apikey.md). `gemini-acp`, `codex-acp` and `claude-acp` take an optional key (T202).

- **Opaque type.** `creds.Key` keeps its value in an unexported field. `String()` and `GoString()` print only how the key was found, and `encoding/json` sees no field, so formatting or marshalling a `Key` cannot leak it. `Secret()` is the only accessor.
- **Status, not value.** The UI, CLI and snapshots receive `creds.Status` (agent, set, source, variable name). The value is read in process by `creds.Resolve` and `apikey.KeyFor`; `creds.StoredKey` is the one function that hands a stored key to a pane: the workspace passes it to `structured.Options.Key`, and the runtime sets it under `Spec.ACPKeyEnv` after the launch check, so no other child sees it.
- **Structured agents.** The key is the stored one, or a key variable the entry's `passEnv` passes. The vendor's variable and `FLOCKDECK_API_KEY` are not read, and a variable exported for another tool does not reach the agent unless `passEnv` lists it.
- **Gemini records its choice.** When the stored key is first used, the runtime calls `authenticate` with the entry's `keyMethod` (`gemini-api-key`) once, and Gemini CLI writes that auth type to `~/.gemini/settings.json`. The key itself is not written there by Flockdeck. A refused key shows the fixed text "The stored key was not accepted", not the agent's words.
- **Resolution order (other specs).** The spec's own env variables, then the store entry for the agent id, then `FLOCKDECK_API_KEY`. Vendor variables (for example a vendor's usual key variable) are consulted only when the entry talks to that vendor's own host (`agent.VendorsOwn`), so a key exported for the vendor is not sent to a user-set gateway.
- **Vendor key stays with the vendor.** `agent.VendorsOwn` is true only for no `baseURL`, or an https URL on the vendor's host (`api.openai.com`, `generativelanguage.googleapis.com`, `api.anthropic.com`). For any other address, `apikey.KeyFor` and `agent.OwnKeyEnv` leave out `OPENAI_API_KEY` and similar. A stored key filed under no agent id is used only when the target is the vendor's own.
- **No echo from the endpoint.** `apikey.CheckKey` reports only the status; some vendors quote part of the key in a refusal. A refused key is `ErrKeyRefused`, an unreachable endpoint `ErrUnreachable`.
- **Redirects.** The check follows a redirect only on the host that was asked and never from https down to http, because the key travels in `x-api-key` or `x-goog-api-key`, which Go does not strip. It stops after 10.
- **Loopback endpoints** need no key; `isLoopback` compares `localhost` ignoring case and uses `IsLoopback` for IPs.
- **File.** [keys.json](/data/keys-file.md) inside the state directory (which is 0700), file mode 0600, written to a temp file then renamed. Encode errors are replaced with a fixed message because the JSON error would carry the value.
- **Reserved ids.** Ids starting with `@` (the TypeSafe key, `@typesafe`) are refused by `Set`/`Clear` and hidden from `Names`, so the agent-keys dialog and `flockdeck keys` neither show nor change it. Only `JevKey()` returns that value, for the client that sends it to the TypeSafe API.
- **Test isolation.** [testiso](/packages/testiso.md) unsets `*_API_KEY`, `*_API_TOKEN` and `*_AUTH_TOKEN` variables and redirects the config dir during tests.

Related: [state directory permissions](/security/state-dir-permissions.md), [FLOCKDECK_API_KEY](/config/flockdeck-api-key.md).
