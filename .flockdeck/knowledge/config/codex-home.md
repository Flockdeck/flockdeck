---
type: Environment Variable
title: CODEX_HOME
description: Codex's own config directory override, read by Flockdeck to find config.toml when classifying where an agent sends data.
resource: internal/workspace/provider.go
tags: [codex, environment]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/provider.go, internal/workspace/toml.go]
---
Codex's variable, not Flockdeck's. `codexConfigOverride` reads `$CODEX_HOME/config.toml`, or `~/.codex/config.toml` when unset. A missing file means no override. A file that cannot be read, is over 1 MB, or cannot be parsed is treated as an override, because it may hold one. The parsed top-level settings plus the profile named by `profile` decide whether the pane's model provider or base URL is another company or a local model. Command-line flags and other Codex environment variables are not read.

See [workspace](/packages/workspace.md).
