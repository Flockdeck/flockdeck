---
type: Package
title: idle
description: Reports how long the machine has had no keyboard or mouse input and whether the screen is locked.
resource: internal/idle
tags: [idle, platform, push]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/idle/idle.go, internal/idle/idle_darwin.go, internal/idle/idle_darwin_parse.go, internal/idle/idle_linux.go, internal/idle/idle_linux_parse.go, internal/idle/idle_other.go, internal/idle/idle_probe.go, internal/idle/idle_windows.go]
---
# internal/idle

`Since() (time.Duration, locked bool, ok bool)` tells the push loop whether somebody is at the desk. The binary is built with `CGO_ENABLED=0`, so each platform uses a different route.

| Platform | Source |
|---|---|
| Windows | `GetLastInputInfo` and `GetTickCount` through `syscall`. `locked` is true when `OpenInputDesktop` or `SwitchDesktop` fails (lock screen or UAC prompt). The tick difference is a `uint32` subtraction, so the 49.7-day wrap works |
| Linux | First that answers: `gdbus` call to GNOME Mutter's IdleMonitor, then `xprintidle`, then `loginctl show-session` using `XDG_SESSION_ID`. Only logind can report locked |
| macOS | `ioreg -c IOHIDSystem` and its `HIDIdleTime` (nanoseconds). Never reports locked |
| Other | Always `ok=false` |

Each external command runs under a 2 second timeout (`probeTimeout`). When `ok` is false the caller falls back to input seen in Flockdeck's own windows. Spawned commands use `sysproc.NoWindow`.

See [idle probe fallbacks](/gotchas/idle-probe-fallbacks.md).
