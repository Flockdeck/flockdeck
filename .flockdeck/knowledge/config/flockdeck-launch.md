---
type: Environment Variable
title: FLOCKDECK_LAUNCH
description: Per-start marker in a pane's environment that hooks send back so late events from a previous process can be dropped.
resource: internal/hooks/hooks.go
tags: [hooks, pane-env]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
- **Default:** unset (hooks from builds that predate it send an empty `launch`).
- **Set by:** the workspace when it starts a pane process; the value is new at each start (`hooks.LaunchEnv`).
- **Read by:** `hooks.Emit`, which copies it to `Event.Launch`. Claude Code passes its environment on to the hooks it runs.
- **Effect:** the workspace drops a hook event whose launch value is not the pane's current one. See [launch token gotcha](/gotchas/hook-launch-token.md).
