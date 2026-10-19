---
type: Package
title: workspace pane and tab moves
description: How the workspace rearranges live panes and tabs without stopping any session.
resource: internal/workspace/move.go
tags: [workspace, layout, tabs, panes]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/move.go, internal/layout/tree.go]
---
# Pane and tab moves

`internal/workspace/move.go` changes where panes are drawn. A pane keeps its process, conversation, directory and scrollback. The geometry comes from [layout](/packages/layout.md) trees, measured against a notional 1000 by 1000 rectangle (`nominalRect`); the browser holds real pixel sizes.

## Operations

| Method | Behaviour |
|---|---|
| `MovePane(pane, target, edge)` | Drops a pane on an edge of another, in the same or another tab. Across tabs the pane is detached first, then `InsertBeside`; the destination is found before detaching |
| `SwapPanes(a, b)` | Exchanges two positions, even across tabs; splits and proportions stay. Focus follows the swapped pane in each tab |
| `MovePaneDir(dir)` | Keyboard move of the focused pane. Refused when no pane is focused or the tab is zoomed. The neighbour is chosen by geometry (`Neighbor`), then swapped |
| `MovePaneToTab(pane, tab)` | Inserts to the right of the destination's focused pane (or its first pane). Moving to its own tab is a no-op |
| `MovePaneToNewTab(pane)` | Refused if the pane is alone in its tab. The new tab goes directly after the old one and stays with the project it was pulled from |
| `MergeTab(id, target, dir)` | Combines two tabs of the same project; the source tab closes and its agents keep running |
| `MergeTabsInProject(target, dir)` | Folds every other tab of the project in, tabs to the left combined on the near side first so the bar order is kept |
| `MoveTab(id, before)` | Reorders within one project; empty `before` means the end of that project's tabs |
| `TilePanes()` | Rebuilds the current tab as an even grid with `layout.Tile`, in current pane order; needs at least 2 panes; clears zoom |

## Details

- Keyboard moves are reversible: `lastKeyMove` records the tab, pane, neighbour, direction and a fingerprint of the pane order (`paneOrder`). Pressing the opposite arrow while nothing else changed swaps back with that same neighbour, because two panes of different sizes may not return to the same place by geometry alone.
- `detachPane` closes the tab through `unlinkTab` when it was the last pane. Otherwise it picks the focus before removing: the neighbour found by trying right, left, down, then up (`paneBesideTheGap`).
- `unlinkTab` picks the next active tab from the tab bar the tab was drawn in (`barOf`), which for a multi-repo project holds every member's tabs.
- `sameProject(a, b)` is true for equal roots or members of one project group. Merges and reorders across projects are refused.
- `landOn` selects the tab, focuses the pane, clears zoom, and switches the active project when the tab belongs to another open one.
- Errors name which side of a drop went away ("the pane it was dropped on is no longer on screen") because that message is all the user sees.

Related: [workspace](/packages/workspace.md), [layout restore rules](/gotchas/layout-restore-rules.md), [layout file](/data/layout-file.md).
