---
type: Package
title: testiso
description: TestMain helper that keeps tests off the real config directory, stored keys and network.
resource: internal/testiso
tags: [testing, isolation]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/testiso/testiso.go]
---
A package adds `func TestMain(m *testing.M) { os.Exit(testiso.Main(m)) }`. `testiso.Main` sets `store.DirGuard = iso.DirGuard` and runs `iso.Main`. The store package's own tests use `internal/testiso/iso` directly (importing testiso would be a cycle).

## What `iso.Main` does

- Creates a temp dir (`flockdeck-test-home-*`) and points APPDATA, LOCALAPPDATA, XDG_CONFIG_HOME, HOME and USERPROFILE at it; unsets CLAUDE_CONFIG_DIR and CODEX_HOME (and HOMEDRIVE/HOMEPATH on Windows).
- Unsets every environment variable ending `_API_KEY`, `_API_TOKEN` or `_AUTH_TOKEN`.
- Unsets every `FLOCKDECK_*` and `PERCH_*` variable and `GIT_CONFIG_COUNT`, except the names in `keptEnv` (switches CI or a person sets to steer tests), `FLOCKDECK_TEST_*`, `FLOCKDECK_ACPTEST_*` and the two real-dir variables. A test that reads a new variable from outside must add it to `keptEnv`; `TestEveryVariableATestReadsFromOutsideIsKept` fails until it does.
- Sets GOCACHE and GOMODCACHE to their real defaults when unset, so tests that run `go build` keep the real caches.
- Removes `flockdeck-test-home-*`, `flockdeck-state-*` and `flockdeck-wait-*` directories (plain directories only, never links) in `os.TempDir()` last changed over 24 hours ago, left by runs that were killed.
- Puts stubs for the catalog's CLI agents (`iso.StubNames`) first on PATH: links of the test binary that `Main` recognises by the name it was started as; they answer `--version`/`--help` and idle until stdin ends. Off when `FLOCKDECK_LIVE_AGENT_TESTS` is set. `ISO_STUB_LOG` makes them log each run. A new catalog agent must be added to `StubNames` (a test fails until it is).
- Replaces `http.DefaultTransport` with a guard that refuses any host but loopback (`ErrNonLoopback`), before name lookup.
- Records every refusal; if the run otherwise passed but a guard tripped, it exits 1.
- `DirGuard` panics when `store` resolves the real config dir or anything under it.
- The real dirs are passed to child test binaries in two env vars, see [testiso real dirs](/config/flockdeck-testiso-real-dirs.md). `ChildEnv()` adds them for hand-built child environments.

Imported by most internal packages' tests and by the `cmd/deploydrift`, `cmd/portproxy`, `cmd/release` and `cmd/sitegen` tests.
