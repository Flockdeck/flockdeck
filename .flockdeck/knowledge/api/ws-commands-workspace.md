---
type: WebSocket Message
title: Control commands: workspace, tabs, panes, projects
description: Commands on /ws/control that change tabs, panes, layout, projects and send prompts.
resource: internal/server/control.go
tags: [websocket, control, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/control.go]
---
Handled in `handleCommand` (`internal/server/control.go`). Part of [/ws/control](/api/ws-control.md). All are refused to viewer devices ([gate](/security/viewer-command-gate.md)). After each, the server wakes the state push, so the reply is a new [`state`](/api/ws-state.md).

## Tabs

| `cmd` | Fields | Effect |
|---|---|---|
| `newTab` | `kind`, `agent`, `model`, `dir`/`root` | New tab with an agent or shell |
| `closeTab` | `id` | Close; refused with a notice if the tab holds locked panes |
| `selectTab` | `id` | Notice "tab gone" if it no longer exists |
| `nextTab`, `prevTab` | | Cycle |
| `renameTab` | `id`, `text` | Empty text goes back to the automatic title |
| `moveTab`, `mergeTab` | `id`, `target` | Reorder, merge into another tab |
| `mergeAllTabs` | | Merge all tabs |

## Panes

`splitPane`, `closePane`, `closeFinishedPanes`, `lockPane` (`locked`), `focusPane`, `restartPane`, `toggleZoom`, `mutePane` (`muted`), `autoReview` (`autoReview`), `movePane`, `swapPanes` (`target`, `edge`), `movePaneDir`, `movePaneToTab`, `movePaneToNewTab`, `tilePanes`, `setWeights` (`weights`), `resize` (`cols`, `rows`), `toggleBroadcast`, `toggleBroadcastMember`, `sendPrompt` (`text`, `submit`; refused with a notice when nothing in the tab is running).

## Projects

`openProject`, `selectProject`, `selectRepo`, `closeProject`, `addRepoToGroup`, `removeRepoFromGroup`, `renameGroup`, `groupProjects`, `renameProject`, `archiveProject`, `reorderProjects`, `removeProject`, `forgetRecent`, `recents` (replies with a `recents` message).

## Session

| `cmd` | Effect |
|---|---|
| `save` | `ws.SaveAll()` |
| `detach` | Keep agents running after the last window closes; replies `{"type":"detached"}` after saving. From a relay window: only a notice |
| `quit` | Save, then request quit. Refused from relay windows. A failed save blocks it unless asked again within 2 minutes |
| `restart` | Same, for restart. Refused from relay windows |
