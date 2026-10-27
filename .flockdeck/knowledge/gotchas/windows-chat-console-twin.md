---
type: Gotcha
title: Windows needs a console-linked twin binary
description: The Windows release ships a console build beside the GUI one for the commands Claude Code runs and the update step.
resource: Makefile
tags: [windows, build]
timestamp: 2026-10-19T00:00:00Z
code_paths: [Makefile, internal/session/launch.go]
---

The Windows release binary is linked `-H=windowsgui` so a shortcut launch opens no console window. A GUI-subsystem program is given no console, so the build also produces `flockdeck-chat.exe`: the same code linked as a console program. The name comes from the removed `flockdeck chat`, the first command to need it; the twin is not a chat client now.

What runs the twin:

- The hook and status-line commands Claude Code runs. Claude Code reads their output through pipes. `session.ChatExe` returns the twin beside the running executable when it exists, and the executable itself otherwise (a plain `go build` makes a console program).
- The update step in `install.ps1`.

`selfupdate` stages and applies the twin with the program ([pending.json](/data/pending-update-file.md)), and `EnsureChatTwin` rebuilds it by setting the PE Subsystem to console.

Related: [Makefile targets](/build/makefile.md), `useConsole` in `console_windows.go`.
