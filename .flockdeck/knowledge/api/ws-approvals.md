---
type: WebSocket Message
title: /ws/approvals socket
description: End-to-end encrypted, relay-only socket on which a paired phone is shown the permission requests of structured agents and allows or denies them.
resource: internal/server/approvals_socket.go
tags: [websocket, approvals, remote, e2e, structured]
timestamp: 2026-10-28T00:00:00Z
code_paths: [internal/server/approvals_socket.go, internal/server/approvals_gate.go, internal/server/approval_card.go, internal/server/artifacts_audit.go]
---
Handler: `Server.handleApprovals`. Route `GET /ws/approvals`. Built on the pattern of [/ws/artifacts](ws-artifacts.md). The exact frame shapes are in `docs/phone-approval-plan.md`, "T3 as built".

## Gates, in order

1. Not through the relay: `404`. No `Flockdeck-Remote-Device` or no `Origin` header: `403`.
2. Per device: 20 sockets opened a minute and 2 open at once; over either, close 4429 `busy`.
3. Encryption is mandatory: a key on both sides, else 4403 `key`. Only the usual key origin: a desk-origin request gets `origin`.
4. `approvalCheck`: approval is on for the device at the current wording (`disabled`), the desk has the key marked verified (`verify`), and `mayApproveWithKey` says the grant was made for the key the handshake proved (`grant`).
5. Handshake (20 s). Then `approvalCheck` runs with the key the session was derived with (`e2e.Session.DeviceStaticKey`); no roster key is read for the decision. Before the handshake only "is approval on for this device" is asked.
6. The `connect` line is written to [remote-approvals.log](/data/approvals-audit-log.md); if that fails, the socket closes with an internal error.

The same check runs before every frame the device sends, before every frame the host sends (replies included), and twice inside a decision: after the pane is read and immediately before `AnswerPermission`. A device switched off is also cut at once (`s.sockets.cut`), which tells a decision in flight (`cutCh`).

## What is sent

Sealed JSON text frames with an `op` field. Every frame, both ways, is padded with a `"pad"` field to a multiple of 1,024 bytes; a request that is not is refused with `error` `padding` and counted as abuse. Requests: `hello`, `ping`, `approvalList`, `approvalDecide`. Replies: `hello`, `pong`, `approvals`, `approvalResult`, `error`. The host sends `approvals` unprompted when a card appears, changes or ends, once the device has sent `approvalList`.

The phone is sent only what `projectCard` returns: cleaned command or path, counts, a canAllow flag and a fixed reason. No agent text, no tool name, and not the agent's request id (the host keeps it against the offer). The phone answers `allow` or `deny`; `phoneAnswer` picks the card's own option.

## Decisions

A decision names an `offer`: a random value made for a card shown on this socket, used once, honoured no sooner than 700 ms after the card was sent. The lock (card number, request id, runtime token) is checked again against the pane, and `AnswerPermission` decides between two answers. The decision line is written to the log before the answer is given; if it cannot be written, nothing is given.

Limits: 5 decisions at once then 1 a second per device (shared by its sockets); 10 frames at once then 2 a second; 3 refusals in a row of the kind a correct client never causes (unknown or used offer, wrong card, bad shape, allow of a card not offered) close the socket with 4403 `refused` and tell the desk; 10 busy replies in a row close with 4429. A tap on an offer already used or whose card ended, a decision too soon, and a refusal for rate do not count as abuse. A success resets the count.
