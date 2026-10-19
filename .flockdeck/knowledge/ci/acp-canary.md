---
type: CI Check
title: acp-canary workflow
description: Weekly report on whether the ACP adapters in the manifest still answer initialize, pinned and latest.
resource: .github/workflows/acp-canary.yml
tags: [ci, agents, acp]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/acp-canary.yml, internal/acp/manifest/canary_test.go]
---

Triggers: Sundays 07:00 UTC and manual dispatch. Never a pull request or a push, so it cannot block a merge; read-only permission, no secrets.

| Job | What it does |
|---|---|
| `contract` | Runs `go test ./internal/acp/...` (the fake-agent contract suite) when `internal/acp/acptest` exists. |
| `handshake` | Matrix of the three manifest adapters by channel `pinned` (manifest version) and `latest` (the `preview` npm tag, else `latest`). Installs the adapter with npm into a new folder under RUNNER_TEMP and runs `TestCanaryInitialize` (build tag `acp_canary`). |

Isolation: the adapter's home, config, cache, temp and npm cache folders and its working directory are all inside that one folder, its environment is only PATH, CI and those folders, and the log prints them. A last step lists files in the runner's real home that changed. Package: [acp manifest](../packages/acp-manifest.md).
