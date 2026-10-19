---
type: Gotcha
title: Atomic state writes and Windows file contention
description: State files are written temp-then-rename with retries because Windows refuses a rename onto a file another process holds.
resource: internal/store/store.go
tags: [store, windows, concurrency]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
- `writeAtomic` takes a per-path in-process lock, skips the write if the file already holds identical bytes (size compare first, then contents), writes a `<name>.tmp*` file, fsyncs, then `renameWithRetry`. It fsyncs the directory except on Windows.
- Retries use exponential back-off from 1 ms up to a 32 ms delay within a 1 second budget (`contentionBudget`). On Windows only a sharing violation (errno 32) counts as "held by another"; other failures return at once. `readState` retries reads the same way.
- The in-process lock does not cover a second instance (for example `flockdeck -solo`), so the retry remains.
- `SweepSessions(maxAge)` deletes orphaned `*.settings.json` files in `sessions/` and any name containing `.tmp`, only if older than `maxAge` (must be positive). While another instance is recorded and alive, settings files are kept because live panes depend on them.

See [store](/packages/store.md).
