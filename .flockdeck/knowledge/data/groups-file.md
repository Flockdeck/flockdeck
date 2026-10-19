---
type: File Format
title: Project groups (groups.json)
description: Persisted multi-repo project groups.
resource: internal/store/store.go
tags: [store, persistence, projects]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
`groups.json` in the config dir: a JSON array of `ProjectGroup`.

| Field | Meaning |
|---|---|
| `id` | Stable across renames and member changes, so a group is found again as members are reopened one at a time. |
| `name` | Hand-chosen display name; empty means derive one. |
| `roots` | Member repo roots in the order added. |
| `primary` | Home member: default cwd for a new tab, default worktree base, naming tie-breaker. |

`LoadGroups` skips entries with a blank id or no roots. A root with no group is a project of one. Corrupt files are set aside as damaged. `SaveGroups` writes `[]` for nil.

See [store](/packages/store.md), [workspace](/packages/workspace.md).
