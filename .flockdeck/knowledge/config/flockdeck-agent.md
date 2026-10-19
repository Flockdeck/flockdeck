---
type: Environment Variable
title: FLOCKDECK_AGENT
description: The id of the agent a pane runs, set in the pane's environment.
resource: internal/session/launch.go
tags: [config, pane, agent]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/launch.go, internal/workspace/workspace.go]
---
Set for agent panes only (`Launch.paneVars` and `Workspace.paneEnv`); a shell pane is told nothing. Lets a script, prompt or an agent that runs `flockdeck spawn` say which agent it is without asking the instance. It is layered last, so a catalog entry's `Spec.Env` cannot override it. It has no legacy `PERCH_` spelling. Companion variables: `FLOCKDECK_MODEL` (the model the pane runs, after the agent default is applied; omitted when empty), `FLOCKDECK_ROLE` (only for a pane started in a role). See [FLOCKDECK_PANE](/config/flockdeck-pane.md).
