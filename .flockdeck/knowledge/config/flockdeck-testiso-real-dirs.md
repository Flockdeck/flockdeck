---
type: Environment Variable
title: FLOCKDECK_TESTISO_REAL_CONFIG and FLOCKDECK_TESTISO_REAL_HOME
description: Variables test binaries use to pass the developer's real config and home directories to child test binaries.
resource: internal/testiso/iso/iso.go
tags: [testing, isolation]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/testiso/iso/iso.go]
---
- **Set by:** `iso.Main` in the first test binary, before it redirects APPDATA, HOME and related variables to a temp dir. They hold the pre-redirect values of `os.UserConfigDir()` and `os.UserHomeDir()`.
- **Read by:** `iso.Main` in a test binary started by another test: if `FLOCKDECK_TESTISO_REAL_CONFIG` is present, the child keeps the environment it was given and treats those values as "real".
- **Use:** `iso.DirGuard` and `UnderRealConfig` compare against them to panic when code resolves the real config dir. Nothing opens that directory.
- `iso.ChildEnv()` returns both for tests that build a child environment by hand.

See [testiso](/packages/testiso.md).
