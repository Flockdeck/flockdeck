---
type: Package
title: layout
description: Tree of splits for a tab's panes, with operations and a Compute step that tiles the tree into cell rectangles.
resource: internal/layout/tree.go
tags: [layout, panes, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/layout/tree.go]
---
Pure in-memory model, no I/O. The persisted mirror is `store.Node` ([layout file](/data/layout-file.md)).

## Types

- `Node`: `ID` (process-unique `n<seq>`, stable for a run only), `Pane` (leaf id), `Dir` (`Horizontal` left to right, `Vertical` top to bottom), `Children`, `Weight` (0 means 1).
- `Rect` {X, Y, W, H} in cells; `Edge` (left/right/top/bottom drop edges); `Direction` for focus movement.

## Invariants kept by every operation

A pane appears once; a split has two or more children; no split nests directly inside a split on its own axis (`Flatten`); `Compute` tiles the given box exactly, with one cell between horizontally adjacent panes (`separatorWidth`) and none vertically (each pane draws a header row).

## Operations

`NewLeaf`, `NewSplit`, `Split`, `InsertBeside`, `MovePane`, `SwapPanes`, `Combine`, `Remove`, `Resize`, `SetChildWeights`, `Compute`, `Separators`, `Neighbor` (directional focus), `PaneAt`, `FindNode`, `Find`, `Leaves`, `Panes`, `Count`.

`Grid(panes)` builds rows of columns, with row count `floor(sqrt(n))` (three panes are one row, twelve are three rows of four), favouring width over height. `Tile(root, box)` rebuilds an even grid of the same panes, keeping each pane's cell if the tab already is a grid.

Used by [workspace](/packages/workspace.md).
