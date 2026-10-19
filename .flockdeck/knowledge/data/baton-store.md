---
type: File Format
title: Baton store files
description: The batons folder in the state directory and the small bookkeeping files used to age batons out.
resource: internal/baton/store.go
tags: [baton, store, retention]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/store.go, internal/baton/shared_other.go, internal/baton/shared_windows.go, internal/baton/prune.go]
---
# Baton store

Folder `batons/` in the state (config) directory, created mode 0700 on first save. Never inside a checkout.

| File | Purpose |
|---|---|
| `<id>.md` | One [baton document](/data/baton.md). Created with `O_EXCL` and mode 0600; saving an existing id returns `ErrExists`. A failed write removes the partial file. Files over 1 MiB are not read |
| `overflow.json` | Baton id to list of paths where the full text of a baton too big for its prompt was written. Those paths are in checkouts, so the store remembers them to clean up |
| `overflow-retries.json` | Path to count of failed removals; given up after 5 |
| `used.json` | Baton id to last-used time, read beside the file's modified time and the time in the id |
| `prune.json` | Latest time pruning has seen, to detect a clock that went backward |
| `inuse/` | One empty file per baton a pane was just started from; protects it for one hour (`leaseFor`) from another Flockdeck instance whose layout is not yet saved |

## Retention

`RetainFor` is 30 days after last saved or used. Pruning runs when a baton is saved and removes older batons and their overflow files unless something still wants them (a saved layout, an `inuse` mark). `ErrClock` stops pruning when the clock went backward, jumped, or a baton is dated in the future. Each overflow removal is given 3 seconds.

`List` returns newest first and skips files that are not batons. On Windows batons are opened with all sharing modes (`openShared`).
