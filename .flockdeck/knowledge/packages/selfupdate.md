---
type: Package
title: selfupdate
description: Finds, downloads, verifies, stages and applies new Flockdeck releases.
resource: internal/selfupdate
tags: [update, security, release]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/selfupdate]
---
Three separate steps: `Latest`/`Newer` ask what is newest; `Stage` downloads, verifies and unpacks into the state dir; `Apply` swaps the staged binary in. Apply is never done under a running session.

## Sources

- The download site (`Site` constant, laid out as in [release manifest](/data/release-manifest.md)) first; GitHub (`Repo` = `Flockdeck/flockdeck`) when the site fails, answers badly or fails a signature. Falling back is logged.
- `Latest`, `List`, `Fetch`, `Recall` (signed `recalled.json`), `CheckManifest`, `CheckPointer`, `CheckRecalled`, `CheckReleaseIndex`.

## Staging and applying

- `Stage` / `StageChosen` (sets `chosen` on the [pending record](/data/pending-update-file.md)) download into a per-attempt `staging.new-*` dir, hash while writing, cap the read at the signed size, then swap into `staging`.
- `Apply` moves the running executable aside and copies the new one in (Windows allows renaming a running exe; copy because volumes can differ). The Windows console twin goes in first and is rolled back if the program fails to install.
- `Sweep` removes moved-aside files on next start; `EnsureChatTwin` rebuilds the twin by setting the PE Subsystem to console (`ConsoleTwin`).
- `Dir(state)` is `<state>/updates`.

## Versions and keys

`Newer`, `Parseable` compare versions; untagged local builds never have a newer release. Signing and trust: `Sign`, `Verify`, `VerifyAny`, `ParseSigningKey`, `TrustedKeys`. The public keys are compiled in (`releaseKey`, `releaseKeyStandby`). See [release signature verification](/security/release-signature-verification.md).

HTTP client timeout is 1 hour (`requestLimit`); a stalled body is aborted after `stallTimeout` (1 minute).

Used by the root program, `internal/server`, `internal/helpers`, [release](/tools/release.md) and [sitegen](/tools/sitegen.md).
