---
type: Environment Variable
title: CLAUDE_CODE_GIT_BASH_PATH
description: Claude Code's Git Bash location on Windows, read so Flockdeck quotes hook and status-line commands for the same shell.
resource: internal/session/claudeshell.go
tags: [claude, windows, environment]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/claudeshell.go, internal/session/claude.go, internal/session/statusline.go]
---
Windows only. `GitBash()` returns, in order: the value if its file name is `bash.exe`, `sh.exe`, `bash` or `sh` and it exists; `C:\Program Files\Git\bin\bash.exe`; the `(x86)` equivalent; `bin\bash.exe` two folders above the `git` found on PATH. If none exists it returns "" and Flockdeck assumes Claude Code uses PowerShell (`-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command`).

The answer decides how a hook command line is written (see [pane settings](/data/pane-settings-file.md)) and how `ClaudeShellCommand` runs the user's own status line command. The shell choice was read from Claude Code 2.1.269's executable.
