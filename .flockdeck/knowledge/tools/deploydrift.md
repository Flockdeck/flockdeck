---
type: Developer Tool
title: deploydrift
description: Reports sibling repos whose main branch has shipping changes no version tag has picked up, and syncs one tracking issue.
resource: cmd/deploydrift
tags: [ci, release, github]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/deploydrift/main.go, cmd/deploydrift/drift.go, cmd/deploydrift/github.go, cmd/deploydrift/issue.go]
---
For flockdeck-relay, -billing, -site and -docs, merging to main deploys nothing; a `v*` tag does. flockdeck-remote reaches users only through a remote tag, a relay dependency bump and a relay tag. `deploydrift` makes the gap visible.

| Flag | Default | Meaning |
|---|---|---|
| `-threshold-hours` | 24 | Age of the oldest untagged shipping commit before a repo is flagged. Must be a non-negative number. |
| `-dry-run` | false | Print the picture and issue body; change nothing. |

## Environment

| Variable | Use |
|---|---|
| `DEPLOY_DRIFT_TOKEN` | Required. Reads the other repos. See [DEPLOY_DRIFT_TOKEN](/config/deploy-drift-token.md). |
| `GITHUB_TOKEN`, `GITHUB_REPOSITORY` | Needed (unless `-dry-run`) to update the tracking issue in this repo. |
| `GITHUB_API_URL` | Default `https://api.github.com`. |
| `GITHUB_STEP_SUMMARY` | If set, the report is appended to it. |

## Behaviour

- Two profiles decide which paths count as not shipped: service repos ignore markdown; site and docs repos treat markdown as shipping. At most 100 per-commit file lookups and 300 commits per repo; commits past the cap count as shipping.
- Any repo that cannot be read aborts the run before the issue is touched, so partial data never closes the issue. A missing token is an error, never a pass.
- Run daily (06:17 UTC) and on manual dispatch by the deploy-drift workflow.
