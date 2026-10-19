---
type: Setting
title: routing.mode
description: Turns model routing off or on, per installation or per project.
resource: internal/agent/routing.go
tags: [routing, agents-json]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent/routing.go, internal/route/route.go]
---
- **Where:** `routing.mode` in [agents.json](/data/agents-json.md), or `projects.<path>.routing.mode`. Set with `agent.SetRouting(dir, project, "mode", value)`.
- **Values:** `off` (default), `auto`. `auto` applies a routed model to a spawned helper or a pane's first turn and shows it in the pane header.
- **Invalid values:** any other value, and the retired `suggest`, are read as `off` with a notice. `suggest` is not mapped to `auto`.
- **Read by:** `route.Router.On()`.
- A project's own policy replaces the shared one whole. An empty mode for a project removes its policy; an empty mode is rejected for the installation-wide policy.
