---
type: Gotcha
title: Windows needs a console-linked twin binary
description: An API agent pane runs the chat client, which needs a console program on Windows.
resource: Makefile
tags: [windows, build]
timestamp: 2026-10-19T00:00:00Z
code_paths: [Makefile]
---

The Windows release binary is linked `-H=windowsgui` so a shortcut launch opens no console window. A GUI-subsystem program is given no console, so the build also produces `flockdeck-chat.exe`: the same code linked as a console program.

A pane is a pseudo-console, which Windows attaches only to console programs. An API agent pane runs the [chat command](/cli/chat.md) as its process, so it uses the console build; run from the GUI build the pane stays blank.

Related: [Makefile targets](/build/makefile.md), `useConsole` in `console_windows.go`.
