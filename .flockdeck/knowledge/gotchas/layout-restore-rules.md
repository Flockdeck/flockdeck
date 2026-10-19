---
type: Gotcha
title: Layout save and restore edge cases
description: Rules in persist.go that keep a restart from losing, duplicating or mis-sizing tabs and projects.
resource: internal/workspace/persist.go
tags: [workspace, persistence, restore]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/persist.go]
---
- Each project saves to its own layout file ([layout file](/data/layout-file.md)). A project not on screen is saved on the tab it was last left on; if none is known the previous file's `active` index is carried forward (clamped), read with `store.Peek` so that a failed read does not clear the "unread file" record and let the save overwrite it.
- `SaveLayouts` runs while the app runs; `SaveAll` adds the open-projects list ([session file](/data/session-file.md)) on exit. Every project is attempted and each error names its project.
- Restore: a layout that cannot be read is noted in `RestoreErrors` and the project opens fresh; the file is later moved aside, not overwritten ([set-aside files](/gotchas/unreadable-state-files-are-set-aside.md)).
- A pane id appearing twice in a layout (damage or hand edit) keeps the first; two processes on one Claude transcript would leave one unreachable.
- Pane size: saved cols/rows apply only if both are positive and at most 4096, and are never smaller than 80x24, because the saved size may come from a narrow phone viewer reached through the relay.
- A split that loses all but one child collapses into that child, which takes the split's share of space.
- Saved focus naming a pane that failed to decode falls to the first pane; an unrestorable active tab falls to the nearest earlier restored tab, then the first.
- A borrowed pane (`root` differs from the tab's) opens its project (`ensureProjectOpen`); an `opening` guard prevents two projects restoring each other forever.
- `RestoreSession` keeps the project the user asked for active, collects all panes from all projects and launches them in one pass (`deferLaunch`), then records the reopened projects as used in one batch behind the requested one. Saved roots are compared loosely (`sessionRootIsOpen`: trailing separator, case on Windows and macOS) so a project is not opened twice.
- A saved project whose folder is missing is not an error: it stays in the list for up to 10 consecutive starts (`maxAwayStarts`, counts in `away`), then is dropped.
- Layouts written before names-by-hand were marked (`handNames` absent): a title that differs from the one the tab would get now is treated as a hand-chosen name with no known automatic title.
- Agent panes resume their conversation on restart where the agent supports it; tasks, batons and roles come back by id.

See [workspace](/packages/workspace.md).
