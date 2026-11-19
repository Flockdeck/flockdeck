# Remote: what a paired phone can do

This page is for people working on Flockdeck. The user-facing text is `ui/help/pages/remote.md`; the wire details are in `.flockdeck/knowledge/api/ws-sealed.md`. The code tables behind both are `sealedCommands` and `sealedMessages` in `internal/server/sealed.go`, listed in `api/catalog.json` under `sealed`.

## Two sockets

A paired phone reaches this machine through the relay on two kinds of connection.

`/ws/control` is TLS to the relay and from the relay to the desktop. The relay can read it and could forge a command on it. So the desktop scrubs every message that leaves on it (default-deny, `relayscrub.go`) and refuses every command that returns conversation text or can do harm. It carries the roster only: pane names, statuses, counts, push, mute, and the start flow for a project that is already open. The sealed work left it as it was, except that it now refuses the commands the sealed socket took over (`startAgent` with a prompt, `commit`, `gitPush`, `gitPull`, `ghPRComment`, `ghIssueComment`, the team commands).

`/ws/sealed` (API 1.37 to 1.42) is end-to-end encrypted between the phone and the desktop. The relay forwards bytes it cannot read or alter. The handshake is the one `/ws/pty` and `/ws/approvals` use: a P-256 key exchange, then AES-256-GCM, with every frame padded to a multiple of 1,024 bytes so frame length does not tell a search from a reply. There is no plaintext fallback. The socket opens only if all of these hold, and each is checked again before every request and before every message written:

- the device has a registered key and the handshake proved it;
- the desk has marked that key verified (the code comparison in the Remote dialog);
- the device may see panes: a full device, or a viewer the desk let watch panes.

A device that fails one is closed with 4403 and a reason (`key`, `origin`, `access`, `verify`, `disabled`). The first frame the desktop sends is `sealedHello`: wire version, API block, the tier this device has and the commands it may send. A command the desktop does not run is answered `command`, so a phone built for a newer desktop is told rather than cut off.

Every request goes through the same `handleCommand` as a window does, with the relay's refusals still in force except the few the sealed table lifts by name. A command not in `sealedCommands` is refused and never reaches it. Every message goes through `sealedWrite`: only the types in `sealedMessages`, every string redacted for secrets (picture bytes left alone), the rewind token and the task board taken out, refusal text cut to a line.

## Tiers

| Tier | Needs | Examples |
| --- | --- | --- |
| A, read | verified key, may see panes | conversation, tool output, pictures, search, changes and diffs, team view, PRs and issues |
| B, act | tier A, a full role, the desk's Control agents switch for this key | send a message, stop, answer a question or plan, start an agent, queue edits |
| C, act with a desk confirm | tier B, then a yes at the desk for that request | commit, push, pull, comment on a PR or issue |
| D, desk only | not reachable | merges, keys, roles, pairing, updates, quit |

### Tier A

Any device the socket serves, a viewer included:

- Read a structured or terminal pane's conversation, tool output, plan and permission cards and pictures, and search it (`structuredSnapshot/Watch/Unwatch`, `conversation*`).
- List a project's changes and branches, a branch's files in pages, a file's hunks and context, and CI status (`changes`, `changesSnapshot`, `diff`, `filesPage`, `diffHunks`, `diffContext`, `ghChecks`).
- Read the push settings (`pushPrefsGet`).
- Unpair itself (`remoteRevoke` with its own id only).

A full device only (a viewer is refused with code `role`, because these name every pane or folder):

- The start flow reads: `recents`, `browse` (inside known projects), `conversations` (history), `worktrees`, `structuredSessions`.
- The team view: `teamSnapshot/Watch/Unwatch`, `graphSnapshot/Watch/Unwatch`, `msgList`. The task board is not sent.
- The device list, cut to name, created, last seen and which one is this device (`remoteDevices`). No keys, roles or other devices' ids.
- GitHub reads: `ghStatus`, `ghPRs`, `ghPR`, `ghIssues`, `ghIssue`, with bodies and comments cut to a size.

### Tier B

A full device with Control agents on:

- Send a message, with pictures; stop a turn; edit, remove, clear or send now the queued prompts; run a one-line shell command in a structured pane (`paneSend`, `attachImage`, `structuredCancel`, `structuredQueue*`, `structuredShell*`). A message is never sent to a shell pane.
- Answer a question card or approve a plan card, once (`permissionAnswer`): allow once or reject once, nothing else.
- Start an agent in a folder inside a known project, optionally in a new worktree on a named branch; resume a past conversation; add a worktree; move a structured pane to another conversation or a clean one (`startAgent`, `resumeConversation`, `worktreeAdd`, `structuredResume`, `structuredNewConversation`).
- Send a message to a pane, name or role from the team view, and mark mail seen (`msgSend`, `msgSeen`). The agent receives it as from the person.
- Change the push settings (`pushNotify`, `pushAnonymous`, `pushDelay`). These apply to every paired device.

### Tier C

Held for a yes at the desk (see below): `commit`, `gitPush`, `gitPull`, `ghPRComment`, `ghIssueComment`.

### Desk only

- Merges and merge approval; running a project's check commands (`runChecks`).
- Tool and edit approvals (those belong to `/ws/approvals`), allow-always answers, the agent mode (`structuredSet`), rewind and fork.
- Removing or pruning a worktree, closing panes or tabs, opening a folder outside the known projects.
- Creating a PR or issue, signing in or out of GitHub.
- Provider API keys, agent defaults and the other settings, updates, quit, restart.
- Anything that changes who can reach or control this machine: pairing links, renaming other devices, roles, the Control agents and approval switches, key verification, enabling or removing Remote.
- The task board and routing rules (T203 removes the board).

## The Control agents switch

It is per device, off for every device including ones that were full before, and set only at the desk: Settings, Remote, the **Control** button beside a device. Turning it on shows what it allows and needs the device's key marked verified. `setDeviceControl` is refused from any window reached through the relay, so a phone cannot grant itself the right.

The grant is stored with the hash of the key it was made for (`DeviceAccess.ControlKey`). `mayControlWithKey` needs both the running record and the preferences file to say yes for the key in use now and a full role. It is asked before every tier B or C request and before every tier B message is written. A refusal is `sealedRefused` with code `control`.

These end the grant and cut the device's sockets: turning the switch off, a changed or unverified key, a role change, unpairing, and the approvals kill switch (`flockdeck remote approvals off`). The device reconnects and is told its new tier in the next hello.

Limits for tier B: 2 requests a second (burst 10) per device. `startAgent`, `resumeConversation` and `worktreeAdd` also share 5 at once, then one every 2 seconds (`busy`). Only `attachImage` may be over 16 KiB, up to 3 MiB. Each act is written to the device log by command, pane and device, with no text.

## The desk confirm for commit, push and pull

Tier C commands are not run when received. `askDeskGit` holds each as a `deskConfirm` for at most 2 minutes, with one open per device and eight in all. A device may ask 3 at once, then one every 20 seconds.

1. The phone sends `commit`, `gitPush` or `gitPull` with a `path` in a known project that is a working tree. A commit needs a message. `ghPRComment` and `ghIssueComment` need a PR or issue number and a body of up to 4,000 bytes.
2. The phone is sent `gitConfirm` with a state: `waiting`, then `allowed`, `denied`, `expired`, or `off` (the right went, the key changed or the socket closed). `busy`, `nodesk` (no window on this machine to ask) and `bad` come at once.
3. Every window on this machine that opened as a page is shown a card: the device name, the repository and branch, for a commit the file count and first line of the message, for a comment the number and the first 300 characters. Each window has its own nonce.
4. The answer, `deskConfirmAnswer`, is taken only from such a window with its nonce, and only once. The device's right is checked again before anything runs.
5. A yes runs that one request with the arguments the device sent, as a remote client, so a git settings or hook warning is still answered at the desk. Nothing is remembered; the next request is asked again.

Over `/ws/control` the same commands are refused (`gitDeskOnly`, `ghDeskOnly`). The device log records `sealed-git` events (asked, then the outcome) by device name and kind, with no message, path or file name.

## Refusal codes

`sealedRefused` carries `req`, `cmd` and a `code`:

| Code | Meaning |
| --- | --- |
| `command` | not a command this socket runs |
| `shape` | not a command, not padded, or too long |
| `busy` | rate limit |
| `role` | needs a full device |
| `control` | needs Control agents, which is off, stale, or not for this key |
| `self` | `remoteRevoke` named another device |

A command that runs and fails answers in its own result type (`teamResult`, `changesResult`, `notice`) in fixed words, not git's or the system's error text.

## Tests, and what they do not cover

The Go tests in `internal/server/sealed*_test.go` run a real handshake against a fake device. The phone client's tests run against a fake desktop. Nothing automated runs a phone through the real relay, so push, the handshake over a real tunnel, the relay's 4 MiB buffer and 4,096-frame limits, and the desk card on a real screen are checked by hand: see [remote-phone-checklist.md](remote-phone-checklist.md).
