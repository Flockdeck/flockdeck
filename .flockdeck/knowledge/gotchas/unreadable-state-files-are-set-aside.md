---
type: Gotcha
title: Unreadable or damaged state files are moved aside, not overwritten
description: Layouts, projects, session, groups and prefs that fail to read or parse are renamed with a suffix so a fresh save cannot destroy them.
resource: internal/store/store.go
tags: [store, persistence, data-loss]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
A state file that cannot be read is treated as empty, and the next save would write over it. The store avoids that loss:

- Read failed (other than not-exist): the path is remembered (`noteRead`). The next `Save*` calls `keepUnread`, which renames the file to `<name>.unread` (or `.unread.1`, `.2`, ... up to 100 via `asideName`). If the rename fails too, the save is refused.
- Parsed but unusable (bad JSON, version zero, version higher than 2): `quarantine` renames to `<name>.damaged[.N]` at load time. A higher version is reported as `KeptNewer`, the rest as `KeptDamaged`.
- `store.TakeKept` returns what was moved aside since its last call, with a sentence for the user (`Kept.Sentence`).
- Aside names are never `.json` or `.tmp`, so nothing reloads or sweeps them.
- `Peek` exists because `Load` clears the unread record: a read-only look at a layout that had failed would otherwise let the timed save overwrite the unreadable file.
- A layout whose `root` differs from the requested one is ignored, not moved.

See [layout file](/data/layout-file.md).
