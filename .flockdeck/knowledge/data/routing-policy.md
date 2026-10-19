---
type: Data Type
title: Routing policy
description: The routing section of agents.json: mode, floor, strategy, crossAgent and ordered rules.
resource: internal/agent/routing.go
tags: [routing, agents-json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go]
---
# RoutingPolicy

Lives in [agents.json](/data/agents-json.md): top-level `routing` for every project, and `projects.<path>.routing` for one project. A project policy replaces the shared one whole. `Catalog.RoutingFor(project)` returns the policy and whether it is the project's own. Evaluated by [route](/packages/route.md).

| Key | Values | Setting doc |
|---|---|---|
| `mode` | `off` (default), `auto` | [routing mode](/config/routing-mode.md) |
| `floor` | `small` (default), `mid`, `top` | [routing floor](/config/routing-floor.md) |
| `strategy` | `balanced` (default), `cost` | [routing strategy](/config/routing-strategy.md) |
| `crossAgent` | bool | [cross-agent routing](/config/routing-cross-agent.md) |
| `rules` | list; absent means built-in rules, empty list means none | below |

## Rule

`{name, tier | (model + agent), when}`. Exactly one of `tier` or `model` (a `model` needs an `agent`). `when`: `task` (case-insensitive regexp), `minWords`, `maxWords`, `files` (globs on paths named in the task), `kind` (`spawn` or `turn`), `agent`. All given fields must hold; first matching rule wins.

## Parse problems

Each becomes part of `Catalog.Notice` and never refuses the file: an unreadable policy means routing off; unknown mode means off (the retired `suggest` mode is also read as off, not auto); unknown floor means top; unknown strategy means balanced; a bad rule is skipped (no tier or model, both, bad tier, model without agent, bad regexp or glob, kind `fanout` or unknown kind); a rule naming an agent not in the catalog is kept but never matches.

`SetRouting(dir, project, field, value)` changes `mode`, `floor`, `strategy` or `crossAgent` and keeps rules and unknown keys. A new project policy starts as a copy of the shared one; an empty `mode` for a project removes its policy.
