---
type: Build Step
title: Release artifacts and publishing
description: Files a release consists of, how they are signed, and the order they are published in.
resource: scripts/publish-downloads.sh
tags: [release, signing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [scripts/publish-downloads.sh]
---

`go run ./cmd/release -version vX.Y.Z -out dist -platforms ...` builds archives per platform. After the per-platform jobs are merged, `-sums` writes `checksums.txt`; `-sign` signs it and writes `manifest.json` (carrying the release notes), the `.sig` files and `latest.json`. The signing key comes from the `FLOCKDECK_SIGNING_KEY` environment variable. See the [release tool](/tools/release.md) and [update signature check](/security/release-signature-verification.md).

Version rule, enforced by both the release and image workflows: `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`. A tag with a suffix is a pre-release.

`scripts/publish-downloads.sh <version> [dist]` publishes to the download site over an S3-compatible API (needs `aws` and `curl`):

1. Checks each archive against `dist/checksums.txt`, and that the checksums, manifest, `latest.json` and signatures exist.
2. Uploads the versioned files, then reads one back through the public address.
3. Uploads `/latest/`, then `latest.json` last, so updaters switch in one step.
4. A pre-release, or a version older than the one `latest.json` names, moves neither.

Required environment: `DO_SPACES_KEY`, `DO_SPACES_SECRET`, `DO_SPACES_BUCKET`, `DO_SPACES_REGION`. Optional: `DO_SPACES_ENDPOINT`, `FLOCKDECK_DL_URL` (default `https://dl.flockdeck.ai`), `FLOCKDECK_CHECK_WAIT` (seconds between read-back tries, default 5).

Versioned paths are cached for a year; `latest.json` and `/latest/` for a minute. `latest.json` is unsigned: it only names a version, whose signed manifest the updater checks. `scripts/purge-downloads.sh` removes published versions ([purge-downloads workflow](/ci/purge-downloads.md)). The pipeline is the [release workflow](/ci/release.md).
