---
type: File Format
title: Generated pane settings (<id>.settings.json)
description: Per-pane Claude Code settings file that makes the agent report lifecycle events to Flockdeck.
resource: internal/session/claude.go
tags: [session, hooks, claude]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/claude.go]
---
Written by `WriteHookSettingsWith` to `<config dir>/sessions/<session id>.settings.json`, mode 0600, and passed to Claude Code as its settings file. Settings are additive: the user's own settings and hooks still load.

## Content

- `hooks`: for each event in `hookEventsFor(version)` (the version comes from asking the `claude` program), one command hook with timeout 5 s.
  - Claude Code 2.1.269 or newer: exec form, command = Flockdeck's binary, args `hook --endpoint <url> --session <id> --event <event>`.
  - Older or unknown version: one command line, quoted per platform (single quotes on Unix; on Windows built for Git Bash or PowerShell with 8.3 short paths).
- `statusLine`: present only when routed through Flockdeck (`StatusLine.Mode` on, or auto when the user already has one); the bridge is `flockdeck statusline`, which posts usage and cost figures and then runs the user's own status line command.

The auth token is never written into the file: hooks and the status line read `FLOCKDECK_TOKEN` from the pane's environment, because command arguments are visible to other users in process listings.

`store.SweepSessions` deletes abandoned `*.settings.json` files, see [atomic writes](/gotchas/atomic-state-writes-and-windows-contention.md). Only Claude panes get this file; `WantsSettings(spec)` says whether a Spec's arguments ask for one.

See [session](/packages/session.md).
