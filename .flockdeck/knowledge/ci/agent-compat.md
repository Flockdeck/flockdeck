---
type: CI Check
title: agent-compat workflow
description: Weekly check that the built-in agent catalog still matches the real agent CLIs and model APIs.
resource: .github/workflows/agent-compat.yml
tags: [ci, agents]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/agent-compat.yml]
---

Triggers: Sundays 06:00 UTC, pull requests that touch `internal/agent/builtin.go`, and manual dispatch.

| Job | What it does |
|---|---|
| `cli-flags` | Matrix over `claude`, `codex`, `gemini`, `aider`, `opencode`, `cursor-agent`. Installs each CLI (a failed install is a warning, not a pass) and runs `go test ./internal/agent/... -run TestBuiltinCLIFlagsAreStillReal/<id>`, which compares the flags Flockdeck passes with the CLI's own `--help`. |
| `live-api-models` | With `FLOCKDECK_LIVE_AGENT_TESTS=1` and vendor API keys from repository secrets, runs `go test ./internal/chat/... -run TestLiveModelsAreAccepted`. |
| `claude-smoke-pane` | Installs `claude` and runs `go test ./internal/server/... -run TestLiveClaudeSmokePane`, a real pane through Flockdeck's hook and resume machinery. |

Related: [agent package](/packages/agent.md), [chat package](/packages/chat.md).
