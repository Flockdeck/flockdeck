---
type: Security Boundary
title: Git program scan before commit, push, pull, fetch
description: Flockdeck lists the programs a repository's git config and hooks would run and warns when they are new or changed.
resource: internal/gitx/programs.go
tags: [security, git, hooks]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/gitx/programs.go, internal/gitx/scanfs.go]
---
Threat in the code's own words: an agent that can write inside `.git` can set hooks, `core.sshCommand`, credential helpers, filters or a gpg program, and Flockdeck would run them as the user outside the agent's permission prompts when Commit or Push is pressed.

## Mechanism

- `gitx.ScanPrograms(dir)` returns a `Report` of `Program` items (`Kind`: setting, hook, unreadable, limit, unscannable). A setting matches the `risky` regexp: `core.sshcommand|hookspath|askpass|gitproxy|alternaterefscommand`, `credential.*helper`, `remote.*.uploadpack|receivepack|vcs`, `filter.*.clean|smudge|process`, `merge.*.driver`, LFS extension and custom-transfer paths, `gpg.*program`, `gpg.ssh.defaultkeycommand`, `hook.*.command`, plus `submodule.*.update` and protocol helper URLs checked by value.
- Hooks are identified by a content hash (`Sum`), so editing a hook changes its ID. `Program.ID()` is a 16-hex SHA-256 prefix over kind, name, value, sum, where and submodule.
- Anything the scan cannot finish becomes an item (`unreadable`, `limit`, `unscannable`) instead of "nothing runs". Only a missing git or a non-repo is an error.
- `Judge(report, known)` warns when nothing was accepted and the repo itself names a program, or when a program's ID is not in the accepted set. Machine-level (system/global) config is not warned about on first sight. An `unscannable` item always warns and is excluded from `AcceptableIDs`.
- Warning text strips control and bidi characters and truncates values (`Program.Line`) so a config value cannot impersonate another entry.
- Accepted IDs are stored outside the repo: [git-programs.json](/data/git-programs-file.md).
- All filesystem access in the scan goes through `scanfs.go` (a test enforces this) because of Windows link/reparse-point hazards.
- Hooks are deliberately not disabled for commit/push/pull/fetch; see [gitx](/packages/gitx.md).
