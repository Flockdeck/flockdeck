---
type: WebSocket Message
title: /ws/sealed socket
description: End-to-end encrypted, relay-only socket that carries a slice of the control vocabulary to a verified paired device: reads (tier A) and, with the desk's Control agents switch, acting on agents (tier B).
resource: internal/server/sealed.go
tags: [websocket, sealed, remote, e2e, structured]
timestamp: 2026-11-16T00:00:00Z
code_paths: [internal/server/sealed.go, internal/server/sealed_control.go, internal/server/sealed_stage8.go, internal/server/sealed_confirm.go, internal/server/e2e.go, internal/server/approvals_socket.go]
---
Handler: `Server.handleSealed`. Route `GET /ws/sealed`. API 1.37 (tier A, cap `sealedControl`), 1.38 (tier B, cap `sealedAct`) 1.39 (start flows, history and worktrees, cap `sealedStart`), 1.40 (changes, diffs and checks, and commit, push and pull behind a desk confirm, cap `sealedGit`) 1.41 (the team view, cap `sealedTeam`) and 1.42 (push preferences, devices and GitHub, cap `sealedSettings`), `remote.Capability` 3. Built on the handshake of [/ws/approvals](ws-approvals.md) and [/ws/pty](ws-pty.md).

## Gates

Not through the relay: `404`. No device or no `Origin`: `403`. Per device 20 opens a minute and 2 sockets. Then: a key on both sides (`key`), the usual key origin (`origin`), the right to see panes, which is a full device or a viewer with watch panes (`access`), the handshake, and the desk's mark that the key the handshake proved is verified (`verify`). Each failure closes with 4403 and the reason. The check runs again before every request and before every frame written; `s.sockets.cut` ends the socket on a role change. A request that is refused but leaves the socket open is answered `sealedRefused` with a `code`: `command` (not in `sealedCommands`), `shape` (not padded, not a command, too long), `busy` (rate limit), `role` (a full-only command from a viewer), `control` (tier B or C without the switch) and `self` (`remoteRevoke` for another device).

## Commands

The device sends control commands as sealed text frames padded to 1,024 bytes. Only `sealedCommands` run (tier A: `structuredSnapshot/Watch/Unwatch`, `conversationOpen/Older/Detail/Search/Close`, `changes`, `diff`, and what the sections below add); anything else is answered `sealedRefused` and never reaches `handleCommand`. A run command is a `controlClient` with `remote` and `sealed` set, so every relay refusal still holds, except those `liftsRelayRefusal` names.

## Tier B: acting on agents

A full device the desk has switched "Control agents" on for (`setDeviceControl`, desk only, needs the usual key verified; `DeviceAccess.Control` with `ControlKey`, the hash of the key it was made for) is told tier `B` in the hello, with the tier B commands in the list. `mayControlWithKey` is the gate: the running record and the preferences file must both say yes for this key and a full role. It is asked again before every tier B request (a refusal is `sealedRefused` with code `control`) and before every tier B message is written. Turning it off, a changed or unverified key, a role change, unpairing and the approvals kill switch end it and cut the device's sockets.

Tier B commands: `paneSend` (never to a shell pane: `allowShell` is forced off), `attachImage` (the only request allowed over 16 KiB, up to 3 MiB, `maxImageRequest` in the hello), `structuredCancel`, `structuredQueueEdit/Remove/Clear/SendNow`, `structuredShellRun/Stop/Remove/Send`, `permissionAnswer` and `startAgent`. A device is let send 2 a second (burst 10), and each one is written to the device log by name, pane and device, with no text. Not offered: `structuredSet`, merges, tool approvals.

`permissionAnswer` here answers a question card or a plan card, once: an option of kind `allow_once` or `reject_once`, a plan only with the ids `allow` and `reject`, and an allow only for an adapter Flockdeck wrote. Any other card (a command, an edit, a tool) is refused: that is the approval path's (`/ws/approvals`).

`startAgent` is refused on the unsealed socket (`relayRefused`): the task is a prompt. On the sealed one it runs for a folder inside a known project, and not as a shell.

## Start flows, history and worktrees (1.39)

Tier A reads, for a full device only (`sealedFullOnly`; a viewer the desk let watch panes is refused with code `role`): `recents`, `browse` (a folder inside a known project; the relay's own rule), `conversations` (the history list of a folder, whose summaries are the first prompts, so redacted), `worktrees` (worktrees and branches of a repo) and `structuredSessions` (the past conversations a structured pane could move to; the one teamResult success a tier A device is sent, titles cut to a line and 200 characters, at most 50).

Tier B acts: `resumeConversation` (a folder inside a known project, the agent that recorded it), `worktreeAdd` (the default path beside the repo only, the relay's `remoteWorktreeOK` rule), `structuredResume` and `structuredNewConversation` (move a structured pane to a past conversation or a clean one; `confirm` still decides whether a running turn is ended). `startAgent` gains `branch` and `base` for a new worktree: both need `worktree: true`, both must pass `plainBranchName` (letters, digits, `.`, `-`, `_`, `/`, 100 characters, no `..`, no leading dash) and then git's own check, and a branch that exists is refused, not renamed. `startAgent`, `resumeConversation` and `worktreeAdd` also share a per-device allowance of 5, then one every 2 seconds (`sealedSpawning`), answered `busy`.

Not on the socket: `worktreeRemove` and `worktreePrune` (they destroy work; they wait for a desk confirm), `structuredRewind`, `structuredFork` and `structuredRewindFiles` (the rewind token is not sent), `newTab`, `openProject`, any folder outside the known projects. There is no agent mode field: a mode can switch off the agent's permission checks, so it stays at the desk (`structuredSet`).

## Changes, diffs and checks (1.40)

Tier A reads, for any device the socket serves: `changesSnapshot` (the branch list of a project; `root` must be a known project, `relayPathGated`), `filesPage`, `diffHunks`, `diffContext` (by pane id) and `ghChecks` (CI status of a folder), next to `changes` and `diff`. Their answers are `changesTeam`, `changesFiles`, `diffHunks`, `diffContext` and `ghChecks`, redacted like everything else. A read that could not be answered is a `changesResult` in fixed words (`busy`, `badRequest`, `noBranch`); git's own error text is not sent. Not on the socket: `changesWatch` (it pushes for as long as the socket is open), `commentAdd`, `commentResolve`, `commentReopen`, `requestReview`, `submitReview`, `runChecks`, `confirmChecks` (a check file's commands are the desk's), `approveMerge`, `unapproveMerge` and `mergeBranch`. Merges stay desk-only.

## Tier C: commit, push, pull (1.40)

`commit`, `gitPush` and `gitPull` are tier C. They need what tier B needs (the desk's Control agents switch for the key, checked again for the request) and then are not run: `askDeskGit` holds them as a `deskConfirm` for at most 2 minutes (`deskConfirmLife`), one open per device and 8 in all, and a device may ask 3 at once, then one every 20 seconds (`sealedAskBurst`, `sealedAskRate`). The device is sent `gitConfirm` with a `state`: `waiting`, then `allowed`, `denied`, `expired`, `off` (the right went, the key changed or the socket closed), or at once `busy`, `nodesk` (no window on this machine to ask), `bad`. Every local window that opened as a page is sent `deskConfirm` (`state: ask`, the device name, the repository and branch, for a commit the file count and the first line of the message, and a nonce of its own); a `done` follows when anyone settles it.

The answer is `deskConfirmAnswer` (`id`, `nonce`, `on`), taken only from a window on this machine that opened as a page and only with the nonce that window was given; a wrong nonce leaves the question open, an answer is used once, and `on` is checked against the device's right again before anything runs. A yes runs that one request with the arguments the device sent (`commit` with its message, files, stamps and `push`), as a remote client, so a git settings or hooks warning is still answered at the desk. Nothing is remembered. The request needs a `path` inside a known project that is a working tree, and a commit needs a message. The same three commands over `/ws/control` through the relay are refused (`gitDeskOnly`). The device log gets `sealed-git` events (asked, then the outcome) by device name and kind, with no message, path or file name.

## Team view (1.41)

Tier A reads, for a full device only (`sealedFullOnly`; a viewer is refused with code `role`, because the roster names every pane of the project): `teamSnapshot`, `teamWatch`, `teamUnwatch` (the roster, counts and mail of a project; `root` is resolved against the open projects, an unknown one is a `teamResult` refusal), `graphSnapshot`, `graphWatch`, `graphUnwatch` (the graph: teams with their goal, nodes with status and live line, what needs the person, edges for messages between panes) and `msgList` (mail, newest first, a message whole by `msgId`). A watch is pushed to this socket only, at most every `teamInterval`, and ends with `teamUnwatch` or the socket. The messages are `team`, `graph`, `messages` and `teamGone` (the reason, one redacted line). The `team` message has its task board taken out (`tasks`, `openHidden`, `finishedHidden`, `boardError`); the board is being removed (T203) and no task command runs here.

Tier B, the person's voice: `msgSend` (a message to a pane, a name or a role of the project, from the person, not from the device) and `msgSeen` (the person's own mark on a message). The answer is the plain success, with no count. They share the tier B limit and the log, which names the command and the device, never the text.

Not on the socket: `taskList`, `taskGet`, `taskAdd` and the other task commands, `teamClose` (it closes panes), `setRouting` and `clearRoutingLog` (who gets a task is the desk's), merges. Goals are the `goal` of a team in the graph; there is no goal command, and the goal tabs of the desktop (`uiPrefs`) are not on the socket. The unsealed socket still refuses all of these (`teamDeskOnly`).

## Settings, devices and GitHub (1.42)

Tier A: `pushPrefsGet` (any served device) answers `pushPrefs`: `off`, `anonymous`, the effective `delaySeconds` and the `defaultSeconds`, `minSeconds` and `maxSeconds` it is held to. `remoteDevices` (full device only) answers `deviceRoster`, built from the desk's list but cut down: per device its name, `created`, `lastSeen` and `current`, plus `self` (this device's id) and `enabled`. No public key, fingerprint, verify state, role, host, plan or the id of another device is sent. `remoteRevoke` is run only with `id` equal to the device's own (`sealedSelfOnly`; any other id or none is refused with code `self`, and the relay is not asked). It shares the tier B rate, is logged as `sealed-self` (`unpaired` or `failed`) by device and name, and is allowed to a viewer too, because it only takes that device's own access away. The answer is `deviceRevoked` (`ok`, or `error` in fixed words), written before the device's key is forgotten and its sockets are cut; the desk is shown a notice.

GitHub reads for a full device (`sealedFullOnly`): `ghStatus` (installed, signed in, account and host), `ghPRs`, `ghPR`, `ghIssues`, `ghIssue`. A pull request or issue is cut to a size (`scrubGHView`: body 20,000 characters, the newest 50 comments, each 4,000) and redacted.

Tier B: `pushNotify`, `pushAnonymous`, `pushDelay` (a delay out of bounds is not a change). Each is answered with `pushPrefs`, changed or not. They change what is pushed to every paired device, so they need the device's Control agents switch like the rest of tier B.

Tier C, held for a yes at the desk with the commit, push and pull flow ([sealed_confirm.go](../../../internal/server/sealed_confirm.go)): `ghPRComment` and `ghIssueComment`. The request needs `path` (a working tree in a known project), `ghNumber` above zero and a non-blank `ghBody` of at most 4,000 bytes. The desk is asked "comment on a pull request" or "comment on an issue" with the number and the first 300 characters of the comment, and a yes posts exactly that. The answer is `gitConfirm` (its `cmd` names the command) and then `ghPR` or `ghIssue`. The unsealed socket refuses both (`ghDeskOnly`).

Not on the socket: `remotePair`, `remoteRename`, `remoteReconnect`, `remoteVerify`, `remoteEnable` and the other commands that change who can reach or control this machine, `setDeviceRole`, `setDeviceControl`, `setDeviceApprove`, `ghPRCreate`, `ghIssueCreate`, `ghLogin`, `ghLogout`, `ghInstall`, merges, other settings (theme, fonts, keybindings, agent defaults, privacy toggles, provider keys) and updates.

## Messages

`controlClient.write` hands every message to `sealedWrite`: the types in `sealedMessages` only, every string redacted (`record.Redact`, picture bytes left alone), `rewindToken` stripped, refusals cut as the relay's are, and whatever a scrub leaves is redacted again (`redactScrubbed`). Tier B messages (`paneSend`, `permissionAnswer`, `attachImageResult`, `agentStarted`, `gitConfirm`, and the plain success of the tier B ops in a `teamResult`, `msgSend` and `msgSeen` among them) are sent only to a device that may act. No state snapshot or agents list is sent. Everything is padded to 1,024 bytes. The first frame is `sealedHello` (version, api block, tier, command list). The tables are in `api/catalog.json` under `sealed`.
