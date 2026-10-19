---
type: Developer Tool
title: release
description: Cross-builds, packages, checksums and signs Flockdeck releases in the format the updater reads.
resource: cmd/release
tags: [release, packaging, signing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/release]
---
Packaging is Go code so that the archive layout lives next to the code that unpacks it ([selfupdate](/packages/selfupdate.md)) and `make package` yields the same bytes as CI, including on Windows.

| Flag | Default | Meaning |
|---|---|---|
| `-version` | `dev` | Version stamped into binaries and file names. |
| `-out` | `dist` | Output directory. |
| `-platforms` | all six | Comma list of `os/arch`: windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. |
| `-sums` | false | Write `checksums.txt` for archives already in `-out` (merging a release built on several machines). |
| `-sign` | false | Sign the built release (`sign.go`). |
| `-base` | `selfupdate.Site` | Base URL for manifest file URLs. |
| `-notes` | none | File of release notes for the manifest. |
| `-test-key` | false | With `-sign`, accept a key the updater does not trust. Never for a release. |
| `-keygen FILE` | none | Write a new signing key to FILE (mode 0600, never over an existing file), print the public key. |
| `-standby` | false | With `-keygen`, make the offline standby key. |

## Outputs

- `flockdeck_<version>_<os>_<arch>` as `.zip` on Windows and `.tar.gz` elsewhere, plus `.deb` and `.rpm` on Linux (built in-process by nfpm from `nfpm.yaml`), a `Flockdeck.app` bundle in the macOS archive, and on Windows a console twin `flockdeck-chat.exe` made from the program's own build.
- `checksums.txt`; after `-sign`: `checksums.txt.sig`, `manifest.json`, `manifest.json.sig`, `latest.json` (see [release manifest](/data/release-manifest.md)).
- Windows builds use `CGO_ENABLED=0`; Linux and macOS need a native cgo toolchain, so CI builds each platform on its own runner and merges with `-sums`.

## Signing checks

`-sign` needs `-version` to parse as a version and the key in [FLOCKDECK_SIGNING_KEY](/config/flockdeck-signing-key.md). Unless `-test-key`, it refuses a key whose public half is not one of `selfupdate.TrustedKeys()`. It verifies every archive against `checksums.txt`, requires every platform's archive to be listed, and reads back its own manifest and pointer through the updater's checkers before writing. See [release signature verification](/security/release-signature-verification.md).

Called from `make package`, the release workflow (build, `-sums`, `-sign` steps) and the Linux packages workflow. See [makefile](/build/makefile.md) and [release artifacts](/build/release-artifacts.md).
