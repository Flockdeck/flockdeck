---
type: CI Check
title: test workflow
description: Format, vet, test and race checks on three operating systems.
resource: .github/workflows/test.yml
tags: [ci, test]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/test.yml]
---

Triggers: push to any branch, any pull request, and `workflow_call` (used by the [release workflow](/ci/release.md) and the [docker-image workflow](/ci/docker-image.md)). Inputs for calls: `ref` (what to check out) and `release` (boolean).

Job `test` runs on `ubuntu-latest`, `windows-latest` and `macos-latest` (`fail-fast: false`):

1. Linux only: installs `libgtk-4-dev` and `libwebkitgtk-6.0-dev`, then fails if `gofmt -l .` lists any file.
2. `go vet ./...`.
3. `go test -count=1 -timeout 20m ./...`. On Linux and macOS this runs `.github/scripts/go-test-once-more.sh`, on Windows an equivalent PowerShell block: the tests that failed are re-run alone once (the whole package if it failed with no failing test to name). A test that passes the second time does not fail the run; a warning annotation and the job summary name the packages and the tests that failed first.
4. Linux and macOS: the same with `-race` and `CGO_ENABLED=1`.
5. On failure, extracts `--- FAIL`, `panic:` and `WARNING: DATA RACE` blocks (30000 bytes at most) into an error annotation.

When `release` is true, `FLOCKDECK_RELEASE_BUILD=1` is set for the tests, which holds the code to release-only requirements such as carrying the release key.

Job `purge-script` (Linux): runs `shellcheck -s sh scripts/purge-downloads.sh`, starts an S3-compatible test server (`moto` 5.2.3 on 127.0.0.1:5055), and runs `go test -run '^TestPurgeScript' ./cmd/release` with `FLOCKDECK_TEST_S3_ENDPOINT` set.

See [flaky-soak workflow](/ci/flaky-soak.md) for repeated runs.
