---
type: Security Boundary
title: Task board permissions
description: The transition table and capacities that decide who may move, assign, edit or prune a task.
resource: internal/tasks/rules.go
tags: [security, tasks, roles]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/tasks/rules.go]
---
# Task board permissions

Actors are built from a pane on the roster and the role it was started with; neither is authenticated beyond the pane id the caller sent. The rules keep agents to their jobs and are no defence against one set on getting round them.

## Capacities

- **Manager:** the user at the window always; a pane in the `manager` role; a pane with no role while the project has no live manager pane.
- **Owner:** the pane the task is assigned to, or any pane in the assigned role while no pane has it.
- **Creator:** the pane that added it.
- **Reviewer:** a pane in the `reviewer` or `tester` role.

## Moves (anything not listed is refused)

| From | To | Who | Note needed |
|---|---|---|---|
| planned | assigned | manager | |
| planned, assigned | in-progress, blocked | manager, owner | |
| planned, assigned, in-progress, in-review, blocked | cancelled | manager, creator | |
| assigned | planned | manager | |
| in-progress | in-review | manager, owner | |
| in-progress | done | manager | |
| in-review | in-progress | manager, owner, reviewer | yes |
| in-review | done | manager, reviewer | |
| in-review | blocked | manager, owner | |
| blocked | planned, assigned | manager | |
| blocked | in-progress | manager, owner | |
| done | in-progress | manager | yes |
| cancelled | planned | manager | yes |

The owner cannot close its own task or a task in review. Moving to `assigned` needs an owner. Other checks: assign is manager only and the task must be open; editing branch or baton is for manager, owner or creator while open; prune is manager only. Refusals say what the move needs and what the actor is.
