---
type: Package
title: testiso
description: TestMain helper that keeps tests off the real config directory, stored keys and network.
resource: internal/testiso
tags: [testing, isolation]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/testiso]
---
A package adds `func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }`. `testiso.Main` sets `store.DirGuard = iso.DirGuard` and runs `iso.Main`. The store package's own tests use `internal/testiso/iso` directly (importing testiso would be a cycle).

## What `iso.Main` does

- Creates a temp dir (`flockdeck-test-home-*`) and points APPDATA, LOCALAPPDATA, XDG_CONFIG_HOME, HOME and USERPROFILE at it; unsets CLAUDE_CONFIG_DIR (and HOMEDRIVE/HOMEPATH on Windows).
- Unsets every environment variable ending `_API_KEY`, `_API_TOKEN` or `_AUTH_TOKEN`.
- Replaces `http.DefaultTransport` with a guard that refuses any host but loopback (`ErrNonLoopback`), before name lookup.
- Records every refusal; if the run otherwise passed but a guard tripped, it exits 1.
- `DirGuard` panics when `store` resolves the real config dir or anything under it.
- The real dirs are passed to child test binaries in two env vars, see [testiso real dirs](/config/flockdeck-testiso-real-dirs.md). `ChildEnv()` adds them for hand-built child environments.

Imported by most internal packages' tests and by the `cmd/deploydrift`, `cmd/portproxy`, `cmd/release` and `cmd/sitegen` tests.
