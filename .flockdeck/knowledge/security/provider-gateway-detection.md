---
type: Security Boundary
title: Provider and gateway detection for batons
description: How Flockdeck decides which company an agent sends data to, failing to "not known" when settings cannot be read.
resource: internal/workspace/provider.go
tags: [security, privacy, baton, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/provider.go]
---
`BatonProvider*` names where an agent sends what it is given, so a handoff to a different company can be flagged and approved.

- API agents: the wire, with ` via <base URL>` when set. CLIs: anthropic (claude), openai (codex), gemini, else the program name. `ProviderHost` and `BatonProviderShown` reduce a URL to its host before showing it, since a base URL can carry a credential.
- Overrides turn the answer into "not known" with a reason. Checked variables per vendor: Claude `ANTHROPIC_BASE_URL` (official host api.anthropic.com), `ANTHROPIC_BEDROCK_BASE_URL`, `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_FOUNDRY_BASE_URL`, `ANTHROPIC_FOUNDRY_RESOURCE`, `CLAUDE_CODE_USE_BEDROCK|VERTEX|FOUNDRY`; Codex `OPENAI_BASE_URL`, `OPENAI_API_BASE`; Gemini `GOOGLE_GEMINI_BASE_URL`, `GEMINI_API_BASE_URL`. Empty, `0`, `false` and the vendor's own host are not overrides. Sources: the agent's own env block, Flockdeck's environment, Claude Code's `env` blocks in the user, managed and project `settings.json`/`settings.local.json`, and Codex `config.toml` (see [CODEX_HOME](/config/codex-home.md), [CLAUDE_CONFIG_DIR](/config/claude-config-dir.md)).
- Managed settings paths: `%ProgramFiles%\ClaudeCode\managed-settings.json` on Windows, `/Library/Application Support/ClaudeCode/managed-settings.json` on macOS, `/etc/claude-code/managed-settings.json` elsewhere, plus a `managed-settings.d` folder (editor backups and hidden files skipped). The code notes these paths come from Claude Code's documentation and were not checked on a managed machine.
- Fail closed: a settings file that cannot be opened, is over 1 MB, is a link to a non-regular file, or does not parse (after removing a BOM, comments and trailing commas) makes the company unknown. Files are read without following links (`readSettingsFile`). Project folders are walked from the cwd up to the git root, at most 8 levels; with no repo only the folder itself is read.
- Comparison helpers lower-case ASCII only (`asciiLower`), so look-alike characters cannot spell an official host.
- Worktrees not yet created are judged from the branch's committed settings (`BranchSettings`) and the checkout they are cut from.

See [spawn limits](/security/spawn-approval-and-helper-limits.md), [workspace](/packages/workspace.md).
