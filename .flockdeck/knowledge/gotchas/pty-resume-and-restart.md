---
type: Gotcha
title: Terminal socket resume, restart and slow viewers
description: How /ws/pty survives pane restarts, drops slow viewers and resumes by offset.
resource: internal/server/pty.go
tags: [terminal, websocket, pty]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/pty.go]
---
- The socket is tied to the pane, not the process. When a session exits, `waitForRestart` polls every 400 ms for a new session; a restart costs no reconnect. The new run starts after a reset (`ESC c`) because the old process may have left alternate screen, mouse or cursor modes on.
- A pane whose process never started (CLI missing) still gets a socket that waits, instead of being turned away and redialled every few seconds.
- A viewer that falls too far behind is dropped by the session; the socket then closes with "stream ended" and the window reconnects and rebuilds from history.
- Resume (`from=`, `epoch=`): only the run the window was watching can be continued; after a restart the next run starts from nothing. The window counts bytes from `offset` and sends that back as `from`.
- Terminal replies (colour queries, cursor position, focus events) are not counted as use of the pane (`isTerminalReply`), or whichever window answered last would take the pane's size.
- A write has 15 s (`writeBudget`). Pings are skipped while a write is in progress, since a ping queued behind a large replay frame made a slow phone get dropped mid-replay and loop.
- Resizes are recorded in the read loop and applied on another goroutine (`applyResizes`) so a busy workspace cannot block a window's keystrokes.
- A watch-only device never repaints the pane (a repaint resizes it by a row).
