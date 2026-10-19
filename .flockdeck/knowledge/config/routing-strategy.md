---
type: Setting
title: routing.strategy
description: Whether work that no rule matches is left alone (balanced) or sent to the cheapest allowed tier (cost).
resource: internal/agent/routing.go
tags: [routing, agents-json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go, internal/route/route.go]
---
- **Where:** `routing.strategy` in [agents.json](/data/agents-json.md).
- **Values:** `balanced` (default; an empty value means this) and `cost`. Anything else is read as `balanced` with a notice.
- **Effect:** rules decide the same way under both. Under `cost`, work with no matching rule moves to the agent's cheapest model at the [floor](/config/routing-floor.md) (`Source` is `fallback`). It never changes agent, and a model with no tier is left alone. Under `balanced` an unmatched task is untouched.
