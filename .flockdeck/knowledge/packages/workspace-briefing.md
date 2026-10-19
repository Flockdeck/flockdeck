---
type: Package
title: workspace pane briefing
description: How the workspace builds the text that tells an agent which pane it is in, who else is running and which commands it can use.
resource: internal/workspace/briefing.go
tags: [workspace, briefing, agent-context, prompt]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/briefing.go, internal/workspace/context.go]
---
# Pane briefing

`PaneContext` (in `context.go`) is a snapshot of one pane's situation. `briefing.go` turns it into markdown. The snapshot is built on the workspace goroutine from state already in memory, with no git or filesystem calls, because it is built while the agent waits on its start hook. The one file read is the task board's counts ([workspace tasks](/packages/workspace-tasks.md)).

## Two delivery paths

| Path | Call | Used when |
|---|---|---|
| Hook reply | `PaneContext.Render()` (`render(false)`) | The agent reports its lifecycle through hooks (`Caps.Hooks`, context mode `hook`) |
| Opening prompt | `Workspace.OpeningPrompt(pane, task, mode)` then `PaneContext.OpeningPrompt()` | Context mode `prompt`, or `task` with a non-blank task |

The prompt form wraps the briefing in `<flockdeck-context>` tags and appends the task after the closing tag. It leaves the "You were started with this task" line out, since the task follows whole. Any other mode, a missing pane, or an empty task in `task` mode returns the task untouched.

The prompt form says the briefing is sent once and gives the snapshot time. It also describes status as read from output, not reported, because `writeCapabilities` is called with `hooked = !viaPrompt`.

## Sections

This pane (directory, branch, worktree or subdirectory of the project root, repo name for multi-repo projects, tab pane count, task, baton); Your role (only for a pane started with a role); The other agents; Your team; Messages; Task board; What Flockdeck can do; Starting agents; Flockdeck from the shell; The user's view. Team, Messages, Task board and the spawn sections need `CanSpawn` (the hook server exists) and a non-shell pane.

## Limits

| Constant | Value | Meaning |
|---|---|---|
| `ownTaskLimit` | 1200 runes | The pane's own task, flattened to one line |
| `siblingTaskLimit` | 160 runes | Another pane's task |
| `maxSiblings` | 10 | Other agents named; the rest are counted in `SiblingsOmitted` |
| `maxTeam` | 12 | Panes in the "Your team" list |

Siblings are ordered: same checkout and same tab, same checkout elsewhere, same tab, then the rest. Exited panes are left out. Only panes of the same project group count.

## Rules the text encodes

- Spawn rights come from the role: none (spawn is not described), listed roles (examples name a role), or any.
- Task assignment commands are shown only to a pane with no role or the `manager` role.
- Shell quoting of the Flockdeck command (`shellWord`) single-quotes anything with `$`, a backtick, a double quote or a doubled backslash, and double-quotes other paths with unusual characters.
- Key names come from `help.Lookup`; an unknown id is printed as the bare id.

Related: [workspace](/packages/workspace.md), [agent](/packages/agent.md), [team](/packages/team.md), [baton](/packages/baton.md), [team messages](/data/team-message.md).
