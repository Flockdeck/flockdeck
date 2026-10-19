---
type: Package
title: baton pruning
description: How stored batons and their overflow files are aged out safely, and how damaged records and a wrong clock stop it.
resource: internal/baton/prune.go
tags: [baton, retention, housekeeping]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/prune.go, internal/baton/store.go, internal/workspace/baton.go]
---
# Baton pruning

Part of [baton](/packages/baton.md). `Store.Prune(maxAge, now, inUse)` runs when a baton is saved (`internal/workspace/baton.go` calls it with `RetainFor`, 30 days). It returns the ids removed. Files layout: [baton store](/data/baton-store.md).

## Age

A baton's last use is the latest of its file modification time, the time in its id (`YYYYMMDD-HHMMSS-xxxxxx`), and its entry in `used.json`. `Touch(id)` sets the file time and the `used.json` entry. Only batons older than `now - maxAge` are candidates.

## What is never removed

- A baton named in any `layout-*` file in the state folder (found by scanning the file text for ids, in 1 MiB pieces), open or closed project alike.
- A baton `inUse` reports (panes started from it).
- A baton with a mark file in `batons/inuse/` newer than one hour (`MarkInUse`, written when a pane starts from it, so another instance on the same machine sees it). Stale marks and marks for missing batons are swept.
- One that looks over five years old (`absurdAge`): left, and said once a day.

At most half of all batons go in one run, oldest first; with fewer than four, one.

## Stopping conditions

Nothing is removed and `ErrClock` or nil is returned when:

- the wall clock differs from the monotonic clock by more than an hour between two looks (`clockWatch`);
- `now` is more than an hour before the newest time recorded in `prune.json`, or more than 366 days after the newest file in the state folder;
- a baton file is dated more than 24 hours ahead.

A damaged record (`used.json`, `overflow.json`, `prune.json`, `overflow-retries.json`, sources) is moved aside as `name.bad`, `.bad.1` to `.bad.99` (never overwritten) and started again, and pruning is held for 24 hours. A record that cannot be read at all is not moved; pruning waits for 30 days of it staying unreadable. The same 30 day wait applies to a saved layout that cannot be opened; afterwards a second, shared-access open is tried and pruning goes on. Reads of a record that fails to parse wait 50 ms once, in case another process is mid-write. Temporary `.record-*`, `.sources-*` and `.overflow-*` files older than an hour are swept unless they are the newest complete copy of an unreadable record.

## Overflow files

`NoteOverflow(id, path)` records where the full text of an over-budget baton was written (see [baton prompt size budget](/gotchas/baton-prompt-size-budget.md)). After a prune, one background cleanup at a time removes the files of removed batons and old or orphaned ones. A file is removed only if it is a regular file named `baton-<id>.md` inside a folder named `flockdeck`, within 3 seconds; a path whose removal hangs is not retried while in flight. After 5 failures the file is forgotten and logged.

Trouble is logged through `SetLogf`, defaulting to stderr.
