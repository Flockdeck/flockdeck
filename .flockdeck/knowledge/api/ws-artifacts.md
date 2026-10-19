---
type: WebSocket Message
title: /ws/artifacts socket
description: End-to-end encrypted, relay-only socket on which a paired device lists and reads this machine's recordings.
resource: internal/server/artifacts_socket.go
tags: [websocket, artifacts, remote, e2e]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/artifacts_socket.go, internal/server/artifacts_consent.go]
---
Handler: `Server.handleArtifacts`. Route `GET /ws/artifacts`.

## Gates, in order

1. Not through the relay: `404`.
2. No `Flockdeck-Remote-Device` header, or no `Origin` header: `403`.
3. Per-device limits: 20 sockets opened per minute, 3 open sockets (`artifacts.MaxSocketsPerDevice`); over either, close code 4429 `busy`.
4. Encryption is mandatory: both sides must have an e2e key and the device's key must be the one the user verified. Otherwise close 4403 with reason `key` or `verify`.
5. `artifactCheck` reads prefs from disk: at least one offered kind switched on and acknowledged at the current wording (`artifactAckVersion` 1), device on the allowlist (`device`), key verified. Else close 4403 with `disabled`, `device` or `verify`.
6. Handshake (20 s), then the key is looked up again and must be byte-equal to the one checked (`verify` otherwise).
7. The `connect` event is written to the [audit log](/data/artifacts-audit-log.md); if that fails the socket closes with an internal error.

These checks repeat on every request, so switching a kind off takes effect on the next frame. Only the `recordings` kind is offered; `links` and `files` are known but cannot be enabled.

## Close codes

4403 refused (reason string: `disabled`, `device`, `verify`, `key`, `unknown ids`), 4429 busy, 4400 protocol (defined, unused in this file).

## Requests (sealed JSON text frames)

Struct `artifactRequest`: `op`, `v`, `kind`, `pane`, `after`, `id`, `cursor`, `max`. There is no path, URL or port field. Request size at most 64 KiB; non-text frames get `unavailable`.

| `op` | Request fields | Reply |
|---|---|---|
| `hello` | | `{op:"hello", v:1, desk, device, kinds, limits:{chunk, request, listItems}}` |
| `list` | `kind`, `after` (cursor) | `{op:"list", kind, items, next}`; recordings are paged 100 at a time, at most 500 items, scan of at most 2000 files newest first. Item: `id`, `kind`, `name`, `project`, `agent`, `started`, `size`, `mtime`, `viewable` |
| `open` | `id`, `kind`, `pane`, `cursor`, `max` | `{op:"data", id, kind, header, entries, next, done, skipped}` |
| `close` | | `{op:"close"}` |
| other | | ignored, no reply |

Errors are `{op:"error", code}` with `code` one of `unavailable`, `busy`, `disabled`, `too_large`, `timeout`; no detail is given. Ids are issued by this socket's registry; more than 5 unknown ids in a minute closes the socket. Reply bytes are limited per device (20 MiB a minute) and requests to 30 per 10 s; 20 busy replies in a row close the socket. Timeouts: 1 minute to the first request, 5 minutes idle, 30 s per request.

Recording files are opened through a root on the recordings folder (no links, no extra hard links, no secret-named files), redacted again and clipped by `recordview`. The first page of a recording (`cursor` 0) writes an `open` audit event. See [artifact gates](/security/artifacts-socket-gates.md).
