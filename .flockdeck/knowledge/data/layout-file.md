---
type: File Format
title: Saved layout (layout-<hash>.json)
description: Per-project JSON file holding tabs, split tree and pane metadata so a restart reopens the same workspace.
resource: internal/store/store.go
tags: [store, persistence, layout]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
One file per project root, in the config dir (`layout-<16 hex>.json`). The hash is FNV-1a over the normalized root (`filepath.Clean`, lower-cased on Windows and macOS), so two spellings of one path share a file. Written by `store.Save`, read by `store.Load` (restoring) or `store.Peek` (read only).

Only the shape is saved. Conversation history stays in the agent's own transcript store and is reattached by pane id.

## State

| Field | Meaning |
|---|---|
| `version` | Schema version, currently 2. `Save` always writes the current value. |
| `tabs` | List of Tab. |
| `active` | Index of the active tab. |
| `root` | Project root; on load, a file whose root differs from the requested one is ignored. |
| `handNames` | True when every tab says whether its title was chosen by hand. |

Tab: `title`, `focus` (pane id), `root` (Node tree), `named` (title chosen by hand), `auto` (title to fall back to).

Node: `pane`, `dir` ("h" or "v"), `weight`, `children`.

## Pane

`id`, `kind` ("agent" or "shell"), `cwd`, `name`, `agent`, `model`, `routed`, `routedFrom`, `routedFromAgent`, `task`, `baton`, `role`, `root` (only when the pane's project differs from the tab's), `conversation` (only after /clear changed the agent's id), `cols`, `rows`, `parent`, `locked`, `recording`, `autoReview` (pointer: absent means use the `autoReviewDefault` preference, false means off).

Absent fields mean the pre-feature meaning; they are all `omitempty`.

## Migration

Version 1 had only "claude" and "shell" kinds; `migrate` rewrites them in memory to agent panes (agent "claude", empty model). Any other version, or an unparseable file, is moved aside (see [state files set aside](/gotchas/unreadable-state-files-are-set-aside.md)). A newer version is kept as `KeptNewer`.

See [store](/packages/store.md).
