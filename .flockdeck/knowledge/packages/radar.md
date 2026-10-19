---
type: Package
title: radar
description: Predicts merge conflicts between panes working in one repository by merging their snapshots in memory.
resource: internal/radar
tags: [radar, conflicts, git, worktrees]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/radar]
---
# internal/radar

Reports which pairs of panes will conflict at merge time while they are still working. The prediction is git's own: each checkout is snapshotted as a commit (`gitx.Snapshot`) and two snapshots are merged in memory (`gitx.MergeTree`), so uncommitted work counts. It says nothing about whether the merged result builds or passes tests.

## API

- `Predict(ctx, dir, scratch, a, b) (paths, clean, err)`; reads `merge-tree -z` output (tree id, then NUL-ended conflicted paths; a `~<commit>` suffix on file/directory conflicts is stripped).
- `Engine` (one per repository, keyed by `gitx.CommonDir`): `Update(ctx, dir, scratch, []Input) Report`, `Conflicts()`, `Retain(live)`, `Empty()`.
- `Input{ID, Ready, Unknown, Commit, Tree, Head, Paths, Dirs}`; `Conflict{A, B, Paths}`; `Report{Conflicts, Behind, Merges}`.

## Rules in the engine

- A pair is merged only if both checkouts are `Ready` and their changed paths (or moved-out directories) meet. A disjoint fan-out runs no merges.
- A conflict is reported only after 2 consecutive counted refreshes (`confirmAfter`), and refreshes closer than `MinGap` (default 5 s) count as one.
- Results are cached by tree and head ids, at most 4096 entries, least recently used evicted.
- At most 50 merges per refresh (`maxMergesPerRefresh`); the rest set `Report.Behind` and the walk resumes where it stopped (`cursor`).
- `Unknown` inputs (snapshot failed or timed out) leave their pairs untouched; a non-ready input drops its pairs.
- One `Update` runs at a time per engine.

See [radar confirmation](/gotchas/radar-confirmation-delay.md). Depends on [gitx](/packages/gitx.md).
