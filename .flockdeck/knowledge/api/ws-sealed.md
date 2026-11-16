---
type: WebSocket Message
title: /ws/sealed socket
description: End-to-end encrypted, relay-only socket that carries a slice of the control vocabulary to a verified paired device: reads (tier A) and, with the desk's Control agents switch, acting on agents (tier B).
resource: internal/server/sealed.go
tags: [websocket, sealed, remote, e2e, structured]
timestamp: 2026-11-16T00:00:00Z
code_paths: [internal/server/sealed.go, internal/server/sealed_control.go, internal/server/e2e.go, internal/server/approvals_socket.go]
---
Handler: `Server.handleSealed`. Route `GET /ws/sealed`. API 1.37 (tier A, cap `sealedControl`), 1.38 (tier B, cap `sealedAct`) and 1.39 (start flows, history and worktrees, cap `sealedStart`), `remote.Capability` 3. Built on the handshake of [/ws/approvals](ws-approvals.md) and [/ws/pty](ws-pty.md).

## Gates

Not through the relay: `404`. No device or no `Origin`: `403`. Per device 20 opens a minute and 2 sockets. Then: a key on both sides (`key`), the usual key origin (`origin`), the right to see panes, which is a full device or a viewer with watch panes (`access`), the handshake, and the desk's mark that the key the handshake proved is verified (`verify`). Each failure closes with 4403 and the reason. The check runs again before every request and before every frame written; `s.sockets.cut` ends the socket on a role change.

## Commands

The device sends control commands as sealed text frames padded to 1,024 bytes. Only `sealedCommands` run (tier A: `structuredSnapshot/Watch/Unwatch`, `conversationOpen/Older/Detail/Search/Close`, `changes`, `diff`); anything else is answered `sealedRefused` and never reaches `handleCommand`. A run command is a `controlClient` with `remote` and `sealed` set, so every relay refusal still holds, except those `liftsRelayRefusal` names.

## Tier B: acting on agents

A full device the desk has switched "Control agents" on for (`setDeviceControl`, desk only, needs the usual key verified; `DeviceAccess.Control` with `ControlKey`, the hash of the key it was made for) is told tier `B` in the hello, with the tier B commands in the list. `mayControlWithKey` is the gate: the running record and the preferences file must both say yes for this key and a full role. It is asked again before every tier B request (a refusal is `sealedRefused` with code `control`) and before every tier B message is written. Turning it off, a changed or unverified key, a role change, unpairing and the approvals kill switch end it and cut the device's sockets.

Tier B commands: `paneSend` (never to a shell pane: `allowShell` is forced off), `attachImage` (the only request allowed over 16 KiB, up to 3 MiB, `maxImageRequest` in the hello), `structuredCancel`, `structuredQueueEdit/Remove/Clear/SendNow`, `structuredShellRun/Stop/Remove/Send`, `permissionAnswer` and `startAgent`. A device is let send 2 a second (burst 10), and each one is written to the device log by name, pane and device, with no text. Not offered: `structuredSet`, commit, push, merges, tool approvals.

`permissionAnswer` here answers a question card or a plan card, once: an option of kind `allow_once` or `reject_once`, a plan only with the ids `allow` and `reject`, and an allow only for an adapter Flockdeck wrote. Any other card (a command, an edit, a tool) is refused: that is the approval path's (`/ws/approvals`).

`startAgent` is refused on the unsealed socket (`relayRefused`): the task is a prompt. On the sealed one it runs for a folder inside a known project, and not as a shell.

## Start flows, history and worktrees (1.39)

Tier A reads, for a full device only (`sealedFullOnly`; a viewer the desk let watch panes is refused with code `role`): `recents`, `browse` (a folder inside a known project; the relay's own rule), `conversations` (the history list of a folder, whose summaries are the first prompts, so redacted), `worktrees` (worktrees and branches of a repo) and `structuredSessions` (the past conversations a structured pane could move to; the one teamResult success a tier A device is sent, titles cut to a line and 200 characters, at most 50).

Tier B acts: `resumeConversation` (a folder inside a known project, the agent that recorded it), `worktreeAdd` (the default path beside the repo only, the relay's `remoteWorktreeOK` rule), `structuredResume` and `structuredNewConversation` (move a structured pane to a past conversation or a clean one; `confirm` still decides whether a running turn is ended). `startAgent` gains `branch` and `base` for a new worktree: both need `worktree: true`, both must pass `plainBranchName` (letters, digits, `.`, `-`, `_`, `/`, 100 characters, no `..`, no leading dash) and then git's own check, and a branch that exists is refused, not renamed. `startAgent`, `resumeConversation` and `worktreeAdd` also share a per-device allowance of 5, then one every 2 seconds (`sealedSpawning`), answered `busy`.

Not on the socket: `worktreeRemove` and `worktreePrune` (they destroy work; they wait for a desk confirm), `structuredRewind`, `structuredFork` and `structuredRewindFiles` (the rewind token is not sent), `newTab`, `openProject`, any folder outside the known projects. There is no agent mode field: a mode can switch off the agent's permission checks, so it stays at the desk (`structuredSet`).

## Messages

`controlClient.write` hands every message to `sealedWrite`: the types in `sealedMessages` only, every string redacted (`record.Redact`, picture bytes left alone), `rewindToken` stripped, refusals cut as the relay's are. Tier B messages (`paneSend`, `permissionAnswer`, `attachImageResult`, `agentStarted`, and the plain success of the tier B ops in a `teamResult`) are sent only to a device that may act. No state snapshot, agents list or graph is sent. Everything is padded to 1,024 bytes. The first frame is `sealedHello` (version, api block, tier, command list). The tables are in `api/catalog.json` under `sealed`.
