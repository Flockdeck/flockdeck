---
type: Data Type
title: Radar snapshot (gitx.Snap and Scratch)
description: A commit capturing a checkout's committed, uncommitted and untracked work, built in a throwaway object store for conflict detection.
resource: internal/gitx/snapshot.go
tags: [git, radar, worktree]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/gitx/snapshot.go, internal/gitx/bases.go]
---
Used by the conflict radar to compare agents working in separate checkouts without touching them.

## Scratch

`NewScratch(commonDir)` makes a temp directory (prefix `scratchPrefix`, under `os.TempDir()`) holding a throwaway object store. The repository's objects are an alternate; new blobs, trees and snapshot commits go to scratch. `Close` removes it; `SweepScratch(age)` removes leftovers. Scratch commands set `core.splitIndex` and `core.fsmonitor` off. The checkout's index, working tree and refs are never written.

## Snap

| Field | Meaning |
|---|---|
| `Commit` | Snapshot commit (what `merge-tree` takes). |
| `Tree` | Exact content id. |
| `Head` | Checkout's commit; merge base comes from heads. |
| `Dirs` | Directories a path was moved out of. |
| `Paths` | Files changed against the merge base with the base branch, both names of a rename, bumped submodules. |
| `Empty` | No change vs base; other fields unset. |
| `Unrelated` | Empty because no shared history with the base. |

`Snapshot(ctx, dir, scratch, base, dirty)` errors: `ErrTooManyFiles` (over `MaxSnapshotFiles`), `ErrMidOperation` (merge, cherry-pick, revert, am, rebase, bisect or unmerged files), `ErrShallow`, `ErrNoBase` (from `ResolveBase`). `MergeTree` merges two snapshots in memory; `MergeTreeSupport`/`CheckMergeTree` probe whether the installed git supports it.

Untested per the code's own note: clean filters/LFS, custom merge drivers, non-cone sparse checkouts, partial clones on git older than 2.44.

See [gitx](/packages/gitx.md), [workspace radar](/packages/workspace.md).
