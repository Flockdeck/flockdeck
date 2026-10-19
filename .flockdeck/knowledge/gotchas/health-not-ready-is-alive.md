---
type: Gotcha
title: A /health that is not ready still means the instance is alive
description: A busy workspace answers /health with 503 and ready=false; callers must wait, not start a second instance.
resource: internal/server/attach.go
tags: [attach, health, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go]
---
Opening a project and starting its agents happens on the workspace goroutine, which can be busy for seconds. `handleHealth` allows 1 s for the workspace to answer. On timeout it returns `503` with a body that still names the app, version and pid and has `ready:false`.

`Probe` treats that as `ErrNotReady` and retries every 250 ms for `busyGrace` (5 s) before giving up. Giving up too early makes the launch clear the instance record and start a rival set of agents next to the first, which splits the agents in two. That is judged much worse than waiting.

`handleOpen` has the same asymmetry: if the project was handed to the workspace but the verdict did not arrive in 10 s it answers `202`, not an error, because the project does open a moment later.
