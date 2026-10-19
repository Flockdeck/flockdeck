---
type: File Format
title: Open-projects session (session.json)
description: Record of which projects were open when Flockdeck last exited.
resource: internal/store/store.go
tags: [store, persistence, session]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
`session.json` in the config dir; `store.LoadSession` / `SaveSession`.

| Field | Meaning |
|---|---|
| `open` | Project roots open at exit. |
| `active` | The active project; cleared if it is not in `open`. |
| `away` | Per-root count of consecutive starts that found the folder missing (USB or network drive). A project stays listed for a few starts before it is dropped. |

`tidySession` cleans each path, drops blank entries and removes duplicate spellings of one root (`sameRoot`), because the restorer would otherwise open the project twice and double its tabs. A file that does not parse is set aside as damaged and the load returns nil.

See [store](/packages/store.md), [projects file](/data/projects-file.md).
