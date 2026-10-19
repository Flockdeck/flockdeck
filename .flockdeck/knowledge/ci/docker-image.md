---
type: CI Check
title: docker-image workflow
description: Builds, scans, signs and pushes the multi-arch container image on a version tag.
resource: .github/workflows/docker-image.yml
tags: [ci, docker, release]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/docker-image.yml]
---

Triggers: push of a tag `v*`, or manual dispatch with a `tag` input. Concurrency group `docker-image`, no cancellation.

1. `test`: calls the [test workflow](/ci/test.md).
2. `image`: validates the tag against the release version pattern. Tags pushed to `ghcr.io/flockdeck/flockdeck`: `<tag>`, `<tag>-<7-char sha>`, and for a non-pre-release also `<minor>`, `<major>` and `latest`. Builds `linux/amd64` and `linux/arm64` with provenance and SBOM, generates an SPDX SBOM (`anchore/sbom-action`), scans with Trivy (fails on fixed HIGH or CRITICAL findings), signs the digest with `cosign sign`, and attests the SBOM with `cosign attest`. The job summary prints a `cosign verify` command.

Image contents: [container image](/build/docker-image.md).
