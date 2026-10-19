---
type: File Format
title: keys.json
description: The state-dir file holding API keys the user saved, as a flat agent-id to key map.
resource: internal/creds/store.go
tags: [secrets, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/creds/store.go]
---
Located in the state directory (see [state directory permissions](/security/state-dir-permissions.md)).

```json
{
  "<agent id>": "<key>",
  "@typesafe": "<key>"
}
```

- Flat object, indented two spaces, trailing newline. Keys are agent ids from the agent specs; ids starting with `@` are reserved (`@typesafe` is the TypeSafe key).
- Mode 0600. Written to a temp file (`keys.json.tmp*`), synced, then renamed with retry (Windows can refuse a rename over a file a scanner or another process holds).
- A missing file is an empty store. Reads use `store.ReadState` (retry on Windows sharing violations). Every set is a read-modify-write of the whole file under a process-wide mutex.
- An unreadable or malformed file is an error rather than an empty store, so a hand-edit mistake does not silently drop the other keys.

Managed by [creds](/packages/creds.md), `flockdeck keys`, and the keys dialog.
