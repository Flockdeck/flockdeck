---
type: CI Check
title: purge-downloads workflow
description: Manual workflow that deletes published release versions from the download site.
resource: .github/workflows/purge-downloads.yml
tags: [ci, release]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/purge-downloads.yml]
---

Manual dispatch only. Inputs: `versions` (space or newline separated), `dry_run` (default true), `confirm` (for a live run, exactly `purge N versions`, N being the number of distinct versions named), `cloudflare` (default false; also purges a Cloudflare zone, needs two secrets).

- `check`: refuses any ref but `main`; validates the versions and the confirmation with `scripts/purge-downloads.sh --check-input`.
- `purge` (environment `purge-downloads`, concurrency group `release`, 90 minutes): refuses any ref but `main`, then runs the script, which is a dry run unless `dry_run` is false. Always writes a summary.

The script is shellchecked and tested against a local S3-compatible server in the [test workflow](/ci/test.md). Storage layout: [release artifacts](/build/release-artifacts.md).
