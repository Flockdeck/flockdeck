---
type: Gotcha
title: Idle detection differs by platform and may give no answer
description: Linux and macOS read idle time by running a short command, and any failure yields ok=false.
resource: internal/idle
tags: [idle, linux, macos, windows]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/idle/idle.go, internal/idle/idle_linux.go, internal/idle/idle_darwin.go, internal/idle/idle_windows.go]
---
- No cgo, so Linux and macOS shell out (`gdbus`, `xprintidle`, `loginctl`, `ioreg`) with a 2 second limit. `Since` is meant to be asked only when a push is due, not on every tick of the wait loop.
- On Linux the first probe that answers is taken whole; readings are not mixed. Mutter and `xprintidle` never report locked. logind answers only when the session is locked or idle (its `IdleHint` turns on after logind's own, longer, threshold), and needs `XDG_SESSION_ID` ([env](/config/xdg-session-id.md)); an SSH session gives no answer.
- macOS never reports locked; locking stops input, which `HIDIdleTime` already shows growing.
- Windows treats a failure to open or switch to the input desktop as locked, which also covers UAC prompts.
- When `ok` is false, callers fall back to input seen in Flockdeck's own windows.
