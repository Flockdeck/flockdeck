---
type: Environment Variable
title: DEPLOY_DRIFT_TOKEN
description: GitHub token used by deploydrift to read the sibling repositories.
resource: cmd/deploydrift/main.go
tags: [ci, secrets, github]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/deploydrift/main.go]
---
- **Default:** unset, which is an error (never a pass).
- **Read by:** `cmd/deploydrift` through `getenv`.
- **Needed scope:** a fine-grained token for the organisation's flockdeck, relay, billing, site, docs and remote repositories with Contents read-only.
- **Supplied by:** a repository secret in the daily deploy-drift workflow, which fails early with an error annotation if it is empty.
- Writing the tracking issue uses `GITHUB_TOKEN` and `GITHUB_REPOSITORY` instead. Threshold can also come from the repository variable `DEPLOY_DRIFT_THRESHOLD_HOURS` (default 24), passed as `-threshold-hours`.

See [deploydrift](/tools/deploydrift.md).
