---
type: Package
title: keybindings
description: User overrides for Flockdeck's own window shortcuts, kept in keybindings.json.
resource: internal/keybindings/keybindings.go
tags: [keybindings, settings, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/keybindings/keybindings.go]
---
Remaps the window's shortcuts (defined in [help](/packages/help.md) `Keys`), not the Claude Code CLI's own `keybindings.json`, which a pane's agent reads.

## File

`keybindings.json` in the config dir: `{"version": 1, "overrides": {"<action id>": "<keys>"}}`. An id mapped to `""` has deliberately no binding (palette only); an absent id uses the default. Version is never used to reject a file. Written with `store.WriteAtomic`; one process-wide mutex.

## API

- `Load`: missing, empty or damaged file returns no overrides (never a startup failure).
- `Effective(overrides)` returns `help.Keys` with overrides applied; `help.Keys` itself is not modified.
- `SetBinding(id, keys)`: rejects unknown ids, range bindings containing "…" (for example Alt+1 to Alt+9), and any chord already held by another action (`ConflictError`, compared by a normalized signature in `signature.go`). Setting the built-in default removes the override so later default changes still reach the user.
- `ResetBinding(id)`, `ResetAll()`.

Depends on [store](/packages/store.md) for the directory.
