---
type: Security Boundary
title: Routing policy source
description: Routing policy is read only from the user's agents.json, never from files inside a repository.
resource: internal/agent/routing.go
tags: [security, routing, spending]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go]
---
# Routing policy source

The `RoutingPolicy` doc comment states the rule: it lives in [agents.json](/data/agents-json.md) in the state directory and nowhere else, "because a cloned repository must not be able to decide what the user's key spends". A project's own policy is keyed by project path inside that same user file.

Related guards:

- `routing.mode` defaults to off; unreadable values mean off ([mode](/config/routing-mode.md)).
- Cross-agent moves are a separate switch, spawn-only, and need the target agent to be ready ([crossAgent](/config/routing-cross-agent.md)).
- Models with no tier are never routed from or to.
- The [routing log](/data/routing-log.md) records rule names and model ids, never task text.
