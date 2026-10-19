---
type: Environment Variable
title: FLOCKDECK_PROJECT and FLOCKDECK_PANE_NAME
description: The pane's project root and display name, set so a shell prompt or script can read them.
resource: internal/workspace/workspace.go
tags: [config, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/workspace.go]
---
Set by `Workspace.paneEnv` for every pane when the hook server exists. `FLOCKDECK_PROJECT` is `rootOf(pane)`, the project the pane belongs to (not necessarily the tab's). `FLOCKDECK_PANE_NAME` is the pane's name at launch. They are meant for the user's own shell prompt, since shell panes get no lifecycle hooks. Both are also exported as `PERCH_PROJECT` and `PERCH_PANE_NAME` for prompts written against the pre-rename names (see [PERCH_ variables](/config/perch-pane-env.md)); alongside `FLOCKDECK_API`, `FLOCKDECK_TOKEN` and [FLOCKDECK_PANE](/config/flockdeck-pane.md).
