---
type: Setting
title: routing.floor
description: The lowest model tier routing may choose.
resource: internal/agent/routing.go
tags: [routing, agents-json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go, internal/route/route.go]
---
- **Where:** `routing.floor` in [agents.json](/data/agents-json.md) (shared or per project).
- **Values:** `small` (default when empty), `mid`, `top`.
- **Invalid value:** read as `top` (the cautious reading) with a notice, so nothing is moved below top.
- **Effect in `route.Decide`:** a rule asking for a lower tier is raised to the floor (the decision's reason says so); a rule naming a model below the floor changes nothing; the cost strategy's fallback picks the floor tier.
