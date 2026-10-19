---
type: Gotcha
title: Unix socket path length and placement
description: sun_path limits and logout behaviour decide where the local channel socket may live.
resource: internal/channel/place_unix.go
tags: [channel, unix, socket]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/channel/place_unix.go]
---
- `sun_path` holds 104 bytes on macOS and 108 on Linux, including the NUL. The channel allows 100 (`maxPath`) and skips a place whose path is longer, which is why a second place under the temporary directory exists.
- The socket is never put in `$XDG_RUNTIME_DIR`: that directory is removed at logout and a detached instance keeps running.
- The file name is the 8-hex instance id plus `.sock`, so a sibling instance's socket is never the same file; on startup stale ones are swept only after strict checks (see [local channel access](/security/local-channel-access.md)). macOS also refuses a connect when the backlog is full, which is why the sweep needs two refusals.
- If the socket vanishes, a 5 second watcher rebinds it, with doubling backoff; after 3 failures in a row `OnLost` tells the windows. The command line re-probes for 15 seconds after finding nothing, so a rebind lands inside that.
- On Windows a pipe is not rebound.
