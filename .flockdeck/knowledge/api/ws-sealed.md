---
type: WebSocket Message
title: /ws/sealed socket
description: End-to-end encrypted, relay-only socket that carries a read-only slice of the control vocabulary (conversation, changes, diff) to a verified paired device.
resource: internal/server/sealed.go
tags: [websocket, sealed, remote, e2e, structured]
timestamp: 2026-11-16T00:00:00Z
code_paths: [internal/server/sealed.go, internal/server/e2e.go, internal/server/approvals_socket.go]
---
Handler: `Server.handleSealed`. Route `GET /ws/sealed`. API 1.37, cap `sealedControl`, `remote.Capability` 3. Built on the handshake of [/ws/approvals](ws-approvals.md) and [/ws/pty](ws-pty.md).

## Gates

Not through the relay: `404`. No device or no `Origin`: `403`. Per device 20 opens a minute and 2 sockets. Then: a key on both sides (`key`), the usual key origin (`origin`), the right to see panes, which is a full device or a viewer with watch panes (`access`), the handshake, and the desk's mark that the key the handshake proved is verified (`verify`). Each failure closes with 4403 and the reason. The check runs again before every request and before every frame written; `s.sockets.cut` ends the socket on a role change.

## Commands

The device sends control commands as sealed text frames padded to 1,024 bytes. Only `sealedCommands` run (tier A: `structuredSnapshot/Watch/Unwatch`, `conversationOpen/Older/Detail/Search/Close`, `changes`, `diff`); anything else is answered `sealedRefused` and never reaches `handleCommand`. A run command is a `controlClient` with `remote` and `sealed` set, so every relay refusal still holds, except those `liftsRelayRefusal` names.

## Messages

`controlClient.write` hands every message to `sealedWrite`: the types in `sealedMessages` only, every string redacted (`record.Redact`, picture bytes left alone), `rewindToken` stripped, refusals cut as the relay's are. No state snapshot, agents list or graph is sent. Everything is padded to 1,024 bytes. The first frame is `sealedHello` (version, api block, tier, command list). The tables are in `api/catalog.json` under `sealed`.
