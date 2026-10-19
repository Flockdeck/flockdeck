---
type: Gotcha
title: A spawn timeout does not mean the spawn failed
description: The spawn client words timeouts and dropped connections so agents do not start a second helper.
resource: internal/hooks/hooks.go
tags: [hooks, spawn, retries]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/hooks/hooks.go]
---
The application answers a spawn only after the worktree is made and the pane is started. A plain "context deadline exceeded" reads as failure, and an agent told that asks again, creating a duplicate helper.

`spawnFailure` therefore says Flockdeck did not answer within the wait and that the helper may still be starting, so look for its pane before asking again. A refused or reset connection (`dial` or `read` operation error) is reported as the application not answering at that address and possibly having closed, since no retry will fix it. Linux reports a reset and Windows a failed read when the app closes as the request arrives; both take this path.

The wait is 60 s, plus 45 s when `batonElsewhere` asks the user to approve ([spawn](/api/hooks-spawn.md)), so approving at the last moment does not time the command out. The package variables `spawnTimeout` and `BatonApprovalWait` exist so tests need not wait.
