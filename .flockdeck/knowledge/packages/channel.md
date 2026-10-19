---
type: Package
title: channel
description: Private per-instance local channel (Unix socket or Windows named pipe) answering identify and window requests.
resource: internal/channel
tags: [ipc, socket, named-pipe, security]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/channel]
---
# internal/channel

A second listener, separate from the loopback HTTP server, reachable only by the same OS user. It speaks HTTP over a Unix domain socket (Linux, macOS) or a named pipe (Windows) and has two verbs: [identify](/api/channel-identify.md) and [window](/api/channel-window.md).

## API

- `Start(Config) (*Channel, error)`. `Config` needs `StateDir`, `PID` and `Window func() string`; `Started`, `Version`, `Logf` and `OnLost` are optional; `Seams` replaces OS calls in tests. An error means no safe place was found and the instance runs without a channel.
- `ID(pid, started)`: the first 4 bytes (8 hex digits) of SHA-256 over `pid/startNanos`. It names the socket or pipe.
- `Dial(path, timeout)`, `PipeName`, `SDDL`, `PipeConfig`.

## Limits

`MaxConns` 8, `MaxHeaderBytes` 4 KiB, `MaxBodyBytes` 1 KiB, `WindowsPerMinute` 10, `WatchEvery` 5 s (a missing socket is rebound), `LostAfter` 3 failed rebinds before `OnLost` is called.

## Placement

- Unix: `<state dir>/c/<id>.sock`, otherwise `<tmp>/flockdeck-<uid>/<id>.sock`. Never `$XDG_RUNTIME_DIR`, because a detached instance outlives logout. Paths over 100 bytes are rejected ([socket path gotcha](/gotchas/channel-socket-path-length.md)).
- Windows: `\\.\pipe\flockdeck-<id>`.

Access rules are in [local channel access](/security/local-channel-access.md).
