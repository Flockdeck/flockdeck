---
type: Environment Variable
title: CLAUDE_CODE_POWERSHELL_RESPECT_EXECUTION_POLICY
description: When set, Flockdeck stops passing -ExecutionPolicy Bypass when it runs a status line command through PowerShell.
resource: internal/session/claudeshell.go
tags: [claude, windows, environment]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/claudeshell.go]
---
Read in `ClaudeShellCommand` on Windows when no Git Bash is found. If the variable is empty, the PowerShell command is run with `-ExecutionPolicy Bypass`; if it is set to any non-empty value that flag is left out, mirroring what Claude Code does.

See [CLAUDE_CODE_GIT_BASH_PATH](/config/claude-code-git-bash-path.md).
