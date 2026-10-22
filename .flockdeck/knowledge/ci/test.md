---
type: CI Check
title: test workflow
description: Format, vet, sharded tests and race checks on three operating systems.
resource: .github/workflows/test.yml
tags: [ci, test]
timestamp: 2026-10-22T00:00:00Z
code_paths: [.github/workflows/test.yml, .github/scripts/shardtests.go, .github/scripts/test-gate.sh, .github/test-weights.txt]
---

Triggers: push to main, any pull request, and `workflow_call` (used by the [release workflow](/ci/release.md) and the [docker-image workflow](/ci/docker-image.md)). Inputs for calls: `ref` (what to check out) and `release` (boolean).

Jobs:

- `changes` (Linux): says whether a pull request changes only documentation, and which platforms run the race detector (Linux always, macOS only outside pull requests).
- `static` (each of `ubuntu-latest`, `windows-latest`, `macos-latest`): installs the GTK packages on Linux, then format (Linux), `go vet ./...`, the build and vet of the next shell, and the tests of the scripts the workflow uses.
- `shard` (each platform, four shares): `go run .github/scripts/shardtests.go -shard I/4 -profile <os> -retry`. Every job computes the same split from the package and test names; slow packages are cut by test name (fnv32a of the name, mod 4). `.github/test-weights.txt` only balances. A failed package or test is run once more; a pass the second time is a warning. Windows shares run `-p 2`.
- `race` (Linux, and macOS outside pull requests, four shares each): the same tool with `-race` and `CGO_ENABLED=1`.
- `test` (each platform, on Linux): the job the branch protection names (`test (ubuntu-latest)`, `test (windows-latest)`, `test (macos-latest)`). It downloads the result files the other jobs leave (`.github/actions/record-result`) and `.github/scripts/test-gate.sh` fails unless every job of its platform ran and passed and the shares of a kind agree on the plan digest. For a documentation-only change it runs the doc-reading tests instead on the Ubuntu name.
- `purge-script` (Linux): runs `shellcheck -s sh scripts/purge-downloads.sh`, starts an S3-compatible test server (`moto` 5.2.3 on 127.0.0.1:5055), and runs `go test -run '^TestPurgeScript' ./cmd/release` with `FLOCKDECK_TEST_S3_ENDPOINT` set.

On Windows the `.github/actions/fast-drive` action moves TEMP and the Go build cache to the D: drive.

When `release` is true, `FLOCKDECK_RELEASE_BUILD=1` is set for the tests, which holds the code to release-only requirements such as carrying the release key.

See [flaky-soak workflow](/ci/flaky-soak.md) for repeated runs.
