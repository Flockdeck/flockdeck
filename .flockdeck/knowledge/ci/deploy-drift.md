---
type: CI Check
title: deploy-drift workflow
description: Daily check that code merged to main in the Flockdeck repositories has been tagged for deploy.
resource: .github/workflows/deploy-drift.yml
tags: [ci, deploy]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/deploy-drift.yml]
---

Triggers: daily at 06:17 UTC and manual dispatch with an optional `threshold_hours`. Runs only in the `Flockdeck/flockdeck` repository, 15-minute timeout.

Fails first if the `DEPLOY_DRIFT_TOKEN` secret is unset. Then runs `go run ./cmd/deploydrift -threshold-hours N`, where N is the dispatch input, else the `DEPLOY_DRIFT_THRESHOLD_HOURS` repository variable, else 24. A repository is flagged once its oldest untagged shipping commit is older than N hours. Tool: [deploydrift](/tools/deploydrift.md).
