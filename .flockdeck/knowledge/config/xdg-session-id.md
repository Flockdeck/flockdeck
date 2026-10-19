---
type: Environment Variable
title: XDG_SESSION_ID
description: Names the logind session that internal/idle asks about on Linux.
resource: internal/idle/idle_linux.go
tags: [idle, linux]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/idle/idle_linux.go, internal/idle/idle_linux_parse.go]
---
- **Read by:** `loginctlIdle`, the third of three idle probes on Linux (after GNOME Mutter's idle monitor and `xprintidle`).
- **Effect:** with it unset the logind probe gives no answer. With it set, `loginctl show-session <id>` is asked for `LockedHint`, `IdleHint` and `IdleSinceHint`; it answers only when the session is locked or idle.
- Set by the desktop session, not by Flockdeck. See [idle](/packages/idle.md).
