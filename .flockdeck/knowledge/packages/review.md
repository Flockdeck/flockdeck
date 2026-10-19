---
type: Package
title: review
description: Auto-review decider that lets read-only Bash tool calls through without a permission prompt.
resource: internal/review
tags: [review, permissions, bash, git]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/review/review.go, internal/review/bash.go, internal/review/gitcheck.go]
---
# internal/review

`Decide(tool, toolInputJSON, cwd) Decision` is consulted on a `PreToolUse` event for a pane with auto-review on (installed through `hooks.SetReviewHandler`; the workspace wires it as `reviewer: review.Decide`). It only ever answers allow or "ask". It never denies and never touches Claude Code's own permission settings. Details: [auto-review rules](/security/auto-review-read-only.md).

- Only the `Bash` tool is judged; Edit, Write, MultiEdit and MCP calls always fall through to the normal prompt.
- `SecretPath(arg)` is exported for the transcript recorder, which uses the same name check to withhold what an agent read from a secret file.
- `gitRunsNothing` is a package variable so tests can replace it.

Imports [gitx](/packages/gitx.md) and `sysproc`.
