---
type: Gotcha
title: Worktree handling for spawned helpers
description: How spawn reuses, creates, names and rolls back git worktrees, and the checks that avoid data loss.
resource: internal/workspace/spawn.go
tags: [worktree, spawn, git, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/spawn.go]
---
- Reuse: `worktreeFor` returns an existing worktree already on the branch, unless git lists it as prunable (folder gone) or it is the main checkout itself, which is refused ("cannot make a worktree of its own"). Otherwise it calls `gitx.AddFrom(repo, DefaultWorktreePath(repo, branch), branch, "")`. Errors distinguish "not a git repository" from other git refusals.
- Location: a sibling of the repository, `<repo>-<branch>`, with `-2`, `-3`... suffixes, never inside the repo (see [gitx](/packages/gitx.md)). A pane whose cwd is outside its project root is therefore a worktree pane, which is how [process records](/data/worktree-procs-file.md) are limited.
- Branch names: `BranchNameFor(task)` gives `agent/<slug>`; letters and digits of any script are kept, other runs become `-`, the slug is cut to 32 characters on a word boundary when one falls after the 8th character, empty becomes `task`.
- Rollback: `PrepareWorktreeNew` returns a `MadeWorktree` (path, whether the worktree and branch were created, start commit). If the helper then fails to start, `DiscardMade` removes only what the call made, and only if untouched: the worktree is kept if `git status` (ignored files included) shows changes, a file walk finds extras (including empty folders), or files are marked assume-unchanged or skip-worktree; the branch is deleted only if still at its start commit (`DeleteBranchAt` passes the expected commit). A pre-existing branch is never touched. The error says what was kept and why.
- Pre-checks: the agent is checked for installation before any worktree is made (`AgentSpecByID`), so a helper that could not start does not leave one behind. A task over 16 KiB is refused (`maxTaskBytes`).
- Placement: `-tab` joins a tab previously made by `spawn -tab` only (never a hand-made one) and rearranges it as a grid; otherwise beside the parent or in a new tab.
- Settings of a not-yet-existing worktree are read from the object store (`BranchSettings`), following a committed `.claude` symlink only when the target is plain and inside the commit; see [provider detection](/security/provider-gateway-detection.md).

See [workspace](/packages/workspace.md).
