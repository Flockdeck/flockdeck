---
type: Security Boundary
title: Claude Code folder trust inheritance
description: Flockdeck copies an existing trust answer from one directory to a new worktree and never invents one.
resource: internal/session/trust.go
tags: [security, claude, worktree]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/trust.go]
---
Claude Code asks once per folder whether it is trusted and records the answer in its own config file (`.claude.json` in `CLAUDE_CONFIG_DIR` or the home directory). A fan-out creates a fresh worktree per child, which would stop each child on that question.

- `InheritTrust(from, to)` refuses unless `IsTrusted(from)` is already true, so it only carries an answer the user already gave. It is a no-op for a target already trusted.
- `IsTrusted` follows Claude Code's own lookup (read from version 2.1.269): the project the directory belongs to (repo root, or for a linked worktree the root of its main repo), then up the folders to the repo root. Anything unreadable or not matched exactly counts as untrusted, so it can only under-trust.
- The read-modify-write of Claude Code's config happens under the lock Claude Code takes for its own saves (`lockClaudeConfig`), because Claude Code saves that file often during a fan-out.
- On Windows the project key uses forward slashes, as Claude Code's own normaliser does.
- The capability is gated by `Spec.Caps.Trust`; only Claude's config shape is implemented, so other agents are untouched (`TrustedFor` returns true, `InheritTrustFor` does nothing).
- The caller offers it to the user as an explicit choice.

See [session](/packages/session.md), [CLAUDE_CONFIG_DIR](/config/claude-config-dir.md).
