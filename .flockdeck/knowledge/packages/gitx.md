---
type: Package
title: gitx
description: Flockdeck's use of git: status, changes, diffs, commit, push/pull/fetch, worktrees, program scan and conflict-radar snapshots.
resource: internal/gitx/worktree.go
tags: [git, worktree, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/gitx/worktree.go]
---
Every git call is an `exec` of the `git` binary through `run.go`; there is no libgit2.

## Files and main API

| File | Provides |
|---|---|
| `status.go` | `StatusOf`, `StatusWithin`, `Status` (branch, head, upstream, ahead/behind, dirty/untracked counts, `Unborn`, `Operation`), `Branches` |
| `changes.go` | `Changes` (porcelain file list with line counts), `Diff` |
| `commit.go` | `CommitAll`, `CommitReviewed` (refuses if the listed files moved, `IsMoved`), `Stamp`, `Reviewed` |
| `remote.go` | `Push` (sets upstream first time), `Pull`, `Fetch`, `Remotes`, `UpstreamOf` |
| `worktree.go` | `List`, `AddNewBranch`, `AddFrom`, `Remove`, `Prune`, `DeleteBranch`, `DefaultWorktreePath`, `WorktreePaths`, `CommonDir`, `Root`, `IsWorktree`, `DefaultBase` |
| `bases.go` | `ResolveBases`, `ChooseBase`, `ResolveBase`, `BaseOf`, `Bases.Fresh` |
| `since.go` | `Since`: files and commit count a branch changed since its merge base |
| `snapshot.go` | `Scratch`, `Snapshot`, `MergeTree` for the conflict radar, see [radar snapshots](/data/radar-snapshot.md) |
| `programs.go` | `ScanPrograms`, `Judge`, see [git program scan](/security/git-program-scan.md) |
| `indexlock.go`, `indexrefresh.go` | index gate and `RefreshIndex`, see [index lock gate](/gotchas/git-index-gate.md) |
| `tree_windows.go` | job object so a timed-out Git for Windows wrapper kills its child git |

## Behaviour

- Timeouts: `commandTimeout` 20 s for local reads; `networkTimeout` and `worktreeTimeout` 10 min for push/pull/fetch/commit and `worktree add`.
- Every command gets `-c core.fsmonitor=` (blank) so a repo cannot run a monitor on status. Commands other than commit, push, pull, fetch and worktree also get `core.hooksPath` set to the null device.
- Network commands get `GIT_SSH_COMMAND=ssh -o BatchMode=yes` unless `GIT_SSH_COMMAND`, `GIT_SSH` or `core.sshCommand` is set; prompts are refused, and errors for missing login, unknown host and rejected key are rewritten to say what to do.
- `DefaultWorktreePath` puts a worktree beside the repo as `<repo>-<branch segment>`, stepping to `-2`, `-3` ... past existing dirs and registered worktrees; `WorktreePaths` also avoids paths chosen earlier in the same call.
- Used by [workspace](/packages/workspace.md) for worktree panes; the worktree process record is in [worktree procs](/data/worktree-procs-file.md).
