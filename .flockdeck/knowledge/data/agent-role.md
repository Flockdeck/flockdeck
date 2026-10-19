---
type: Data Type
title: Agent role
description: A job an agent can be started for: its briefing, worktree policy, default model and who it may spawn.
resource: internal/agent/roles.go
tags: [agent, roles, spawn]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/roles.go, internal/agent/catalog.go]
---
# Role

A role is not an agent. The agent and model it names are used only when a spawn names none.

| Field | JSON | Meaning |
|---|---|---|
| `ID` | `id` | Lowercased and trimmed |
| `Name`, `Description` | `name`, `description` | Shown where roles are chosen; the description is not given to the agent |
| `Agent`, `Model`, `Tier` | `agent`, `model`, `tier` | Defaults for a spawn; `tier` stands in for `model` (first model of that tier that the chosen agent lists). A model with no agent is read as the project default agent's |
| `Briefing` | `briefing` | Text for the "Your role" part of the pane briefing |
| `Worktree` | `worktree` | `own`, `readonly` or `main`. Guidance only: Flockdeck does not cut or lock a checkout because of it |
| `Spawns` | `spawns` | Role ids this role may start; empty means none |
| `Plain` | `plain` | May start agents that have no role (off by default so it is not a way round `spawns`) |

## Built-in roles

| Id | Worktree | Notes |
|---|---|---|
| `manager` | main | Plain; spawns planner, worker, reviewer, tester, docs. Breaks work into board tasks and starts helpers; does not edit code |
| `planner` | readonly | Writes steps; changes nothing |
| `worker` | own | Carries out one task on a branch |
| `reviewer` | readonly | Reports problems; does not edit |
| `tester` | own | Writes and runs tests |
| `docs` | own | Writes documentation |

## Spawn rule (`Catalog.MayStart`)

A parent with no role, or whose role was removed from the file, is not limited. Otherwise a child role must be in the parent's `spawns`, and a child with no role needs `plain`. `mergeRoles` reports a `spawns` entry that is not a role, a bad worktree policy (dropped) and a bad tier (dropped).
