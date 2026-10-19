---
type: CI Check
title: release-followups-dry-run workflow
description: Manual dry run of the docs regeneration that follows a release.
resource: .github/workflows/release-followups-dry-run.yml
tags: [ci, release]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/release-followups-dry-run.yml]
---

Manual dispatch with a `tag` input (an existing release, such as `v0.3.40`). Job `docs` runs `.github/scripts/regen-followup.sh` in `MODE=dry-run` for the docs repository and compares the result. It pushes nothing and touches no other repository. The real run is the `followups` job of the [release workflow](/ci/release.md); the generator is the [docgen tool](/tools/docgen.md).
