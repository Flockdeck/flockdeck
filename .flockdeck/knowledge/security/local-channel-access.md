---
type: Security Boundary
title: Local channel access control
description: Operating-system and peer checks that restrict the Unix socket or named pipe to the same user.
resource: internal/channel
tags: [security, ipc, socket, named-pipe]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/channel/channel.go, internal/channel/peer_darwin.go, internal/channel/peer_linux.go, internal/channel/peer_other.go, internal/channel/place_unix.go, internal/channel/place_windows.go]
---
# Local channel access

Access is decided by the operating system before a request is read, then checked again per connection.

## Unix (Linux, macOS)

- The directory must be owned by the current uid with mode exactly 0700; `ensureDir` creates it 0700 and verifies it. The socket must be a socket owned by that uid with mode 0600 (`checkSocket`). Both are re-verified when the watcher checks the socket.
- Two places are tried (state dir `c/`, then `<tmp>/flockdeck-<uid>/`); a place failing any check is skipped, and with both failing there is no channel.
- Before binding, a leftover at the path is removed only if it is a socket owned by this uid. Removing on close only removes the file this channel made (matched by file id).
- A stale-socket sweep removes `<8 hex>.sock` files of the same owner only after two refused connects 200 ms apart and a one-minute grace period.
- Peer: Linux reads `SO_PEERCRED`; macOS has its own `peer_darwin.go`.

## Windows

- Named pipe `\\.\pipe\flockdeck-<id>` with security descriptor `D:P(A;;GA;;;<user SID>)`: protected list, full access for the current user only. Without an explicit list a pipe grants Everyone read access.
- `go-winio` creates the first instance with `FILE_CREATE` and rejects remote clients, so a squatter on the name is never joined.
- Peer user is read by impersonating the client on the thread and reading the thread token, after the client has written something.

## All platforms

- Every connection's peer owner must equal the instance's own (uid or SID) or it is closed; refusals are logged at most once a minute with a count.
- At most 8 connections at once; headers 4 KiB; body 1 KiB; `/window` limited to 10 per minute.
- Only `POST /identify` and `POST /window` exist ([identify](/api/channel-identify.md), [window](/api/channel-window.md)). The window link is minted by the same function the HTTP server uses.
