---
type: File Format
title: Accepted git programs (git-programs.json)
description: Record of which git hooks and config-launched programs the user accepted for each repository.
resource: internal/store/gitseen.go
tags: [store, persistence, git, security]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/gitseen.go]
---
`git-programs.json` in the config dir. Per repository (keyed by the normalized common git directory) a `GitSeen` {`ids` (IDs from `gitx.ScanPrograms`), `accepted` (time)}. `LoadGitSeen` returns the value and whether one exists.

It lives in Flockdeck's state dir, not in the repository, so an agent that writes git configuration cannot also write the record that it was accepted. An in-process mutex serialises read-modify-write.

See [git program scan](/security/git-program-scan.md), [store](/packages/store.md).
