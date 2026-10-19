---
type: File Format
title: Recent projects (projects.json)
description: Global list of projects the user has opened, with name, archive flag and manual order.
resource: internal/store/store.go
tags: [store, persistence, projects]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go, internal/store/recentswriter.go]
---
`projects.json` in the config dir. A JSON array of `Project`, kept apart from per-project layouts so the picker can list projects before any is opened.

| Field | Meaning |
|---|---|
| `root` | Project directory. |
| `lastUsed` | Time of last use. |
| `name` | Display name chosen by hand (`SetProjectName`). |
| `archived` | Hidden from ordinary picker lists, still on disk (`SetProjectArchived`). |
| `order` | Position set by `ReorderProjects`, ascending from 1; 0 sorts after ordered projects, by `lastUsed`. |

- Capped at 40 entries (`maxRecents`); the most recently used survive.
- Edits hold `recentsMu` from read to the in-memory change. The file write happens on one background goroutine (`recentsWriter`), which writes the newest pending state, so a burst of changes is one write.
- `AllProjectMeta` returns name, archived and order keyed by the normalized root (`MetaKey`) for the workspace to cache.
- Unreadable or damaged files are set aside, not overwritten.

See [store](/packages/store.md), [session file](/data/session-file.md), [project groups](/data/groups-file.md).
