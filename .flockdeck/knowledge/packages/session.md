---
type: Package
title: session
description: One pane's process on a PTY: launch, output fan-out and replay, status tracking, hook settings, trust inheritance and process-tree shutdown.
resource: internal/session/session.go
tags: [session, pty, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/session.go, internal/session/launch.go, internal/session/background.go, internal/session/trust.go, internal/session/claude.go, internal/session/status.go, internal/session/turn.go, internal/session/history.go, internal/session/assist.go, internal/session/statusline.go, internal/session/replies.go, internal/session/stream.go, internal/session/claudeshell.go, internal/session/shim_windows.go, internal/session/kill.go, internal/proctree/proctree.go, internal/proctree/tree_unix.go, internal/proctree/tree_windows.go, internal/session/usage.go, internal/session/usage_linux.go, internal/session/usage_other.go, internal/session/usage_windows.go]
---
Terminal emulation happens in the browser; this package owns the process, fans output out to viewers and tracks what the agent is doing. PTY via `go-pty`.

## Main API

| Symbol | Role |
|---|---|
| `Launch` / `Launch.Config()` | Resolves an `agent.Spec`, model, pane id, cwd and prompt into a `Config` (argv, env, settings file). API agents run `flockdeck chat` (on Windows the console twin `flockdeck-chat.exe` beside the exe, see [Windows chat twin](/gotchas/windows-chat-console-twin.md)) |
| `Start(Config)` -> `*Session` | Spawn on a PTY; `Kind` is `KindAgent` (alias `KindClaude`) or `KindShell` |
| `Subscribe`, `SubscribeFrom(epoch, off)`, `Unsubscribe`, `Write`, `WriteString`, `Resize`, `Close` | Viewer fan-out and input |
| `Status` | starting, working, waiting, blocked, idle, exited; `NeedsAttention` is waiting or blocked |
| `ApplyEvent(Event)`, `SetStatus*`, `ResolveBlocked`, `CompactionStatus` | Hook-driven status |
| `NoteBackgroundWork`, `BackgroundWork`, `BackgroundEnded` | Background tasks; unverified after 30 minutes without a sign of life |
| `Usage` | CPU share (exponentially weighted average, 15 s time constant, sampled every 5 s) and RSS across the process tree |
| `Look`, `Settings`, `WriteHookSettings*`, `StatusLine` | Binary lookup, generated settings, status-line bridge |
| `TrustedFor`, `InheritTrust*` | Claude Code folder trust, see [folder trust](/security/claude-folder-trust-inheritance.md) |
| `StatusAssist` | Optional Jev-assisted idle vs waiting call, see [status assist privacy](/security/jev-status-assist-privacy.md) |
| `Conversations`, `RecentReplies` | Read stored conversations through `session/transcript` |

## Constants worth knowing

Replay buffer 512 KiB per pane; per-viewer backlog 512 chunks or 2 MiB, past which replay is cheaper; resize clamped to 2000x2000; bell grace and startup grace 5 s; no-hook quiet-before-idle 3 s; after a keyboard-answered tool question 10 s; resize echo 1 s; close grace 3 s; output drain grace 500 ms; patterns read the last 512 bytes only.

## Environment of a pane

`EnvStripping` removes Claude session markers (`agent.ClaudeStripEnv` plus the catalog union), `FLOCKDECK_BASE_URL/WIRE/KEY_ENV` (and legacy `PERCH_` forms), and on non-Windows terminal-identity variables (`TERM_PROGRAM`, `TMUX`, ...), then sets `TERM=xterm-256color`, `COLORTERM=truecolor`. Later layers win and duplicates are dropped, so a catalog entry cannot shadow `FLOCKDECK_*`. See [pane environment](/config/flockdeck-pane.md).

Windows specifics: npm `.cmd` shims are run as `node script` directly; other batch files go through `cmd.exe` with a hand-built command line, shortened to 8191 (batch) or 32767 characters; pane processes live in a job object; `KillTree` ends console processes only. On Unix a pane is a session leader; close sends hangup to the session, then kills. See [pane shutdown](/gotchas/pane-process-tree-shutdown.md).

Settings file: [pane settings file](/data/pane-settings-file.md). Consumer: [workspace](/packages/workspace.md).
