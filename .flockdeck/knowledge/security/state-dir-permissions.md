---
type: Security Boundary
title: State directory permissions
description: The config directory is created 0700 and state files 0600 because they hold the local server token.
resource: internal/store/store.go
tags: [security, store, filesystem]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
- `store.Dir` creates `<user config dir>/flockdeck` with mode 0700 and `makePrivate` narrows an existing directory that is group- or other-accessible (skipped on Windows, where `os.Chmod` does not map to ACLs).
- `store.SessionsDir` (`sessions/` below it, 0700) holds generated per-session agent settings.
- `writeAtomic` writes via `os.CreateTemp`, which creates the file 0600, then renames over the target. Packages with their own files in the dir use `store.WriteAtomic` for the same mode.
- The directory holds the local server auth token (see [instance file](/data/instance-file.md)).
- `store.DirGuard` is a test hook: test builds set it to refuse the developer's real config dir. It is nil in the program.

See [store](/packages/store.md).
