---
type: Environment Variable
title: CLAUDE_CONFIG_DIR
description: Claude Code's own config directory override, read by Flockdeck to find transcripts, trust answers and settings.
resource: internal/session/transcript/claude.go
tags: [claude, environment]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/transcript/claude.go, internal/session/trust.go, internal/workspace/provider.go]
---
Not a Flockdeck setting: it is Claude Code's, which Flockdeck reads so it looks where Claude Code looks. Unset or empty means `~/.claude` (and `~/.claude.json` for the trust file).

| Reader | Uses |
|---|---|
| `session/transcript.claudeHome` | Directory holding Claude Code's stored conversations. A pane's own `Spec.Env` entry for `CLAUDE_CONFIG_DIR` (name compared case-insensitively on Windows) overrides the process value for that pane |
| `session/trust.go` `configPath` | `<dir>/.claude.json` for folder trust, see [folder trust](/security/claude-folder-trust-inheritance.md) |
| `workspace/provider.go` `claudeSettingsEnv` | `<dir>/settings.json` env block, checked to see which provider or gateway a Claude pane talks to |
