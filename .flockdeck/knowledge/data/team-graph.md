---
type: Data Type
title: Team graph
description: The teams, nodes and waiting-on kinds derived from one project's panes and tasks.
resource: internal/team/graph.go
tags: [team, graph, waiting-on]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/team/graph.go]
---
# Graph

Built by `BuildGraph(panes, tasks)` in [team](/packages/team.md). It only reads its arguments.

## Shape

`Graph{teams, nodes, solo}`. A node has `pane`, `parent`, `children`, `depth`, `team`, `orphaned`, `waitingOn`. A team is `{id, root, members}` with members root first, then breadth first.

## Rules

- A pane id counts once (first kept).
- A pane naming a missing parent, or itself as parent, is orphaned and a root. Parent loops are cut at the earliest pane in input order, which becomes an orphaned root.
- A team root is a pane with no parent that has children, or has the `manager` role. Other parentless panes without children are solo.

## `waitingOn` (first match wins)

| kind | When |
|---|---|
| `down` | Pane exited or failed (text `exited` or `failed`) |
| `user` | Activity is waiting or blocked and the pane has a prompt; text is the prompt, plain and at most 200 characters |
| `reviewer` | Non-shell pane owns a task in `in-review`; ref is the task id |
| `task` | Non-shell pane owns a `blocked` task; text is its last note |
| `helpers` | Children that are working or waiting; text `N helpers are busy` |
| `none` | Otherwise |
