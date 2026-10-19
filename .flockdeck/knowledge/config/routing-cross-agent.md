---
type: Setting
title: routing.crossAgent
description: Lets a routing rule move a spawned helper to another agent, not only to another model.
resource: internal/agent/routing.go
tags: [routing, agents-json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go]
---
- **Where:** `routing.crossAgent` in [agents.json](/data/agents-json.md). Default false. `SetRouting` accepts `true`, `false` or empty.
- **Why a separate switch:** writing a rule that names another agent's model must not by itself start spending through a different login.
- **Effect in `route.Decide`:** a rule with `model` and `agent` for another agent matches only when this is on, the work is a `spawn` (never a turn or running pane), and the caller's `OtherAgent` resolver says that agent is usable now (installed or keyed, trusted, endpoint answering). Otherwise such a rule is skipped as if it were about someone else's work, so later rules still get a turn.
