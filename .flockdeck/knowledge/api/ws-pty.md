---
type: WebSocket Message
title: /ws/pty terminal socket
description: One WebSocket per pane streaming raw terminal bytes down and keystrokes up, with optional resume, size frames and end-to-end encryption.
resource: internal/server/pty.go
tags: [websocket, terminal, pty]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/pty.go]
---
Handler: `Server.handlePTY`. Route `GET /ws/pty?id=<pane id>`.

## Query parameters

| Param | Effect |
|---|---|
| `id` | pane id. `404 no such pane`; `503 the workspace is busy` if the lookup times out (5 s, `paneLookup`) |
| `from`, `epoch` | resume: send only the bytes from this offset of this run. Answered with a stream header text frame ahead of each run |
| `size=1` | window wants `{"size":{"cols":N,"rows":N}}` text frames telling it the pty size |

## Frames

| Direction | Frame | Content |
|---|---|---|
| server to window | binary | terminal output, coalesced up to 256 KiB (64 KiB through the relay), at least 8 ms apart |
| server to window | text, only if `from=` was sent | `streamHeader` JSON: `epoch`, `offset`, `resumed`, `end` |
| server to window | text, only if `size=1` | `{"size":{"cols":..,"rows":..}}` |
| window to server | binary | keystrokes written to the pane's session |
| window to server | text JSON | `{"resize":{"cols","rows"}}` and/or `{"focus":true}` (`ptyControl`) |

Read limit 4 MiB. The socket outlives the process: after a restart it follows the pane's new session, first writing the reset sequence `ESC c`. It closes with "pane closed" when the pane goes, or "stream ended" when the viewer fell too far behind (the window reconnects and rebuilds from history).

## Auth and roles

- Token, cookie or relay mark, else `403`. No origin patterns (see [origin check](/security/websocket-origin-check.md)).
- A relay device that is a viewer without `watchPanes` is refused with `403` before the pane is looked up. A viewer that may watch gets output only; its input, resizes and focus are read and discarded, and it never triggers a repaint. The role is checked on each frame.
- Relay connections: per-frame compression without context takeover.

## End-to-end encryption (relay only)

If `E2ECapable(device, origin)` is true, the first frame the window sends must be a binary handshake hello; the server answers with one binary response frame, then every frame in both directions is a sealed `internal/e2e` frame carrying a one-byte tag (0 binary, 1 text) plus payload (`e2eConn`). A failed handshake closes with a policy violation rather than falling back to plaintext. When either side has no key on file the terminal is served unencrypted. `Flockdeck-Remote-Origin: desk` selects the second key. See [relay trust](/security/relay-trust-and-e2e.md).

## Sizing

Each connection reports its size; the pane is sized to the window used last (typing or focus), measured with a 120 ms settle (`resizeSettle`). Pings every 30 s unless a write is in flight.
