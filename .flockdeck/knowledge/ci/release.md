---
type: CI Check
title: release workflow
description: Tag-triggered pipeline that tests, builds, signs and publishes a release.
resource: .github/workflows/release.yml
tags: [ci, release]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/release.yml]
---

Triggers: push of a tag `v*`, or manual dispatch with a `tag` input (the tag must already exist). Permissions: `contents: write`.

Jobs, in order:

1. `test`: calls the [test workflow](/ci/test.md) with `release: true`.
2. `version`: fails unless the tag matches the version pattern in [release artifacts](/build/release-artifacts.md). For a tag without `-rc.`, also fails if `docs/releases/<tag>.md` is missing.
3. `build`: matrix of `ubuntu-latest` (linux/amd64), `ubuntu-24.04-arm` (linux/arm64), `windows-latest` (windows/amd64 and arm64), `macos-latest` (darwin/amd64 and arm64). Runs `go run ./cmd/release -version TAG -out dist -platforms ...`. On Windows it also installs NSIS, builds both GUI binaries, fetches the WebView2 bootstrapper and builds `flockdeck_<tag>_windows-installer.exe`. Uploads `dist-<os>` artifacts.
4. `release` (environment `release`, concurrency group `release`): merges artifacts, writes checksums (`-sums`), builds the notes with `.github/scripts/release-notes.sh`, signs (`-sign`, key from the `FLOCKDECK_SIGNING_KEY` secret), then publishes to GitHub and to the download site with `scripts/publish-downloads.sh` read from the workflow's own commit.
5. `followups` (environment `release`, `continue-on-error`, only for tags without `-`): proceeds only if the tag is GitHub's latest release. Mints a GitHub App token for the private site repository and the docs repository, then runs `.github/scripts/regen-followup.sh` for each, which regenerates that repository from this one and opens a pull request ([site generator](/tools/sitegen.md), [docs generator](/tools/docgen.md)). On failure it opens or comments on an issue titled `Release follow-ups failed for <tag>`.

Publish step behaviour: a pre-release gets `--prerelease`; if a newer non-pre-release is already GitHub's latest, the new release is created with `--latest=false` and the latest flag is restored if GitHub moved it. An existing release gets files uploaded with `--clobber` and keeps its notes. If GitHub cannot be asked, nothing is published.

Notes source order in `release-notes.sh`: existing GitHub release body, then `docs/releases/<tag>.md`, then (for `-rc.` tags only) GitHub's generated notes; otherwise an error.

`.github/scripts/regen-followup.sh` takes all input from environment variables (`TAG` must be exactly `vX.Y.Z`, `TARGET` site or docs, `MODE` apply or dry-run, `AUTO_MERGE`). Branches are named `auto/regen-*`. See [release-followups-dry-run](/ci/release-followups-dry-run.md).
