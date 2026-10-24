---
type: CI Check
title: flaky-soak workflow
description: Nightly repeated test runs of the concurrency-heavy packages, with an issue reporting flakes.
resource: .github/workflows/flaky-soak.yml
tags: [ci, test, flaky]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/flaky-soak.yml]
---

Triggers: daily at 03:17 UTC (only in `Flockdeck/flockdeck`), manual dispatch, and pull requests touching `.github/flakysoak/**` or the soak and report workflows.

Dispatch inputs: `packages` (default `./internal/server ./internal/session ./internal/workspace ./ui ./internal/hooks ./internal/remote`), `count` (default 5, passed as `go test -count`), `dry-run`.

Jobs:

- `tool` (10 minutes): format, vet and test of the `.github/flakysoak` Go module (`main.go`, `parse.go`, `report.go`).
- `soak` (up to 240 minutes): runs the package list with `-count` on each OS and uploads the output as an artifact.
- `report`: calls `reusable-flaky-report.yml` with a pinned commit of the flakysoak tool, the OS labels expected to upload (a night missing one is not counted clean), and `close-after` (default 3 clean nights in a row close the issue). With `dry-run` it writes only the job summary.

Related: [test workflow](/ci/test.md).
