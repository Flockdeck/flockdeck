---
type: Gotcha
title: Concurrent git status and diff can fail on the index on Windows
description: gitx serialises index-rewriting git commands with a gate and reader/writer wait limits.
resource: internal/gitx/indexlock.go
tags: [git, windows, concurrency]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/gitx/indexlock.go]
---
`git diff` and `git update-index --refresh` rewrite the index when stat data is stale (diff does so regardless of `GIT_OPTIONAL_LOCKS`). A `git status` opening the index at that moment fails on Windows with "index file open failed: Permission denied". `Changes` runs status and diff together, and the header refresh runs `update-index` while a review panel is open.

Alternatives were measured and rejected (disabling `diff.autoRefreshIndex`, or taking turns), so the commands run side by side under an `indexGate`:

- Readers (status, numstat, diff) do not wait behind a writer that is only queued, and wait for a running writer for a bounded time (`indexReaderWait`) before running anyway; a read refused the index is retried (`retryIndex`, git messages forced to English via `LANGUAGE=C LC_MESSAGES=C` so the error text matches).
- `RefreshIndex` waits at most `indexWriterWait` for readers, else returns `ErrIndexBusy` to be retried later. It is never killed; after `indexStopWaiting` the gate is released and git finishes and removes its own lock. No other code removes a lock.
- After repeated busy runs a drain period holds new readers so the refresh can get in.
- Every git command run through `run.go` gets `GIT_TERMINAL_PROMPT=0` and `GIT_OPTIONAL_LOCKS=0`, so status polling never holds the lock an agent's commit needs, and never writes back what it learned.
- Not covered: other programs' git, including an agent's.

See [gitx](/packages/gitx.md).
