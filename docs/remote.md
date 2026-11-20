# Remote: what a paired phone can do

This page is for people working on Flockdeck. The user-facing text is `ui/help/pages/remote.md`; the wire details are in `.flockdeck/knowledge/api/ws-sealed.md`. The code tables behind both are `sealedCommands` and `sealedMessages` in `internal/server/sealed.go`, listed in `api/catalog.json` under `sealed`.

## Two sockets

A paired phone reaches this machine through the relay on two kinds of connection.

`/ws/control` is TLS to the relay and from the relay to the desktop. The relay can read it and could forge a command on it. So the desktop scrubs every message that leaves on it (default-deny, `relayscrub.go`) and refuses every command that returns conversation text or can do harm. It carries the roster only: pane names, statuses, counts, push, mute, and the start flow for a project that is already open. The sealed work left it as it was, except that it now refuses the commands the sealed socket took over (`startAgent` with a prompt, `commit`, `gitPush`, `gitPull`, `ghPRComment`, `ghIssueComment`, the team commands).

`/ws/sealed` (API 1.37 to 1.42) is end-to-end encrypted between the phone and the desktop. The relay forwards bytes it cannot read or alter. The handshake is the one `/ws/pty` and `/ws/approvals` use: a P-256 key exchange, then AES-256-GCM, with every frame padded to a multiple of 1,024 bytes so frame length does not tell a search from a reply. There is no plaintext fallback. The socket opens only if all of these hold, and each is checked again before every request and before every message written:

- the device has a registered key and the handshake proved it;
- the desk has marked that key verified (the code comparison in the Remote dialog);
- the device may see panes: a full device, or a viewer the desk let watch panes.

A device that fails one is closed with 4403 and a reason (`key`, `origin`, `access`, `verify`, `disabled`). A live socket is looked at again every 30 seconds against the relay's roster, and it is closed with 4403 and `roster` if the device is no longer listed with a key, and `rekeyed` if it is listed with a key other than the one the socket handshook with (paired again under the same id; it reconnects, and is let in only if the desk has verified the new key). The check reads a cached roster (`e2eRoster`): a key the host has seen for the device is trusted for 10 seconds, so an unpaired device can stay up for that long after the relay drops it. When the relay cannot be reached, a key already cached for the device is used whatever its age, so a relay outage alone does not close sockets of devices the host has seen; a device whose key was never cached is closed with `roster` in that case, and a socket stays up through an outage even if the device was unpaired during it, until the relay answers again. A socket that reaches its maximum age (12 hours) is closed with code 4410 and the reason `renew`: that is not a refusal, and the phone reconnects at once without telling the person. `disabled` means the desk ended the socket (a role change, a verification taken back, the device unpaired, or Control switched off). The first frame the desktop sends is `sealedHello`: wire version, API block, the tier this device has and the commands it may send. A command the desktop does not run is answered `command`, so a phone built for a newer desktop is told rather than cut off.

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

Tier B is equivalent to remote code execution on this machine. A device with Control agents can run shell commands in a structured pane (`structuredShell*`), start an agent with any first prompt (`startAgent`), and type into any pane it can see (`paneSend`), and none of these is confirmed at the desk. Whatever an agent in that pane can do, the device can make it do. The only gate is this switch, so turn it on only for a phone you would trust with a terminal on this machine. Tier C (commit, push, pull, GitHub comments) is the only tier a person at the desk confirms.

It is per device, off for every device including ones that were full before, and set only at the desk: Settings, Remote, the **Control** button beside a device. Turning it on shows what it allows, needs the device's key marked verified, and needs a one-use challenge the host gives that window for that device (`approveChallenge` with `kind` `control`), so a command sent blind or replayed does nothing. `setDeviceControl` is refused from any window reached through the relay, so a phone cannot grant itself the right.

The grant is stored with the hash of the key it was made for (`DeviceAccess.ControlKey`). `mayControlWithKey` needs both the running record and the preferences file to say yes for the key in use now and a full role. It is asked before every tier B or C request and before every tier B message is written. A refusal is `sealedRefused` with code `control`.

These end the grant and cut the device's sockets: turning the switch off, a changed or unverified key, a role change, unpairing, and the approvals kill switch (`flockdeck remote approvals off`). The device reconnects and is told its new tier in the next hello.

Limits for tier B: 2 requests a second (burst 10) per device. `startAgent`, `resumeConversation` and `worktreeAdd` also share 5 at once, then one every 2 seconds (`busy`). Only `attachImage` may be over 16 KiB, up to 3 MiB. A device is let attach 48 MiB an hour, charged only once the pane is known to be open. Its pictures in a pane's folder are kept to 20 files and 64 MiB, the oldest going first; the cap and the trimming cover only that device's own pictures (their file names carry a tag made from the device id), never the desk's or another device's. Each act is written to the device log by command, pane and device, with no text.

## The desk confirm for commit, push and pull

Tier C commands are not run when received. `askDeskGit` holds each as a `deskConfirm` for at most 2 minutes, with one open per device and eight in all. A device may ask 3 at once, then one every 20 seconds. Each question the desk answers no to makes the device wait before it may ask again: 20 seconds, doubling for each further no within an hour, up to 15 minutes. A question left to lapse does not count as a no, so nobody being away from the desk does not push the wait to 15 minutes. It costs 20 seconds, and each further lapse with no yes in between (within an hour) doubles that, up to 5 minutes. A yes clears both. A request made during a wait is answered `wait` with `waitSecs`, not `busy`.

A push or pull that is approved runs from what was pinned at the ask, not from the repository's settings at the yes. A push is `git push -- <pinned push URL> <sha>:refs/heads/<branch>` with `push.default`, `push.followTags`, `push.pushOption` and `push.recurseSubmodules` set aside; a pull is `git pull --ff-only -- <pinned fetch URL> <pinned branch.<b>.merge>` with `pull.rebase` set aside. The remote name, both URLs, the branch, its upstream and the head are read again immediately before git starts, and nothing runs if any differs. A commit with a push sends exactly the commit that step made: it is read straight after the commit, must have the approved head as its only parent (or be a root commit on a branch with none), and the push is refused if HEAD is anything else by then. The commit and that read are two git calls, so a commit an agent wrote in the gap would be the one read.

The pull card shows the URL the pull fetches from (`remote.<name>.url`) and the ref it merges, and holds to them; it does not show the push URL. A remote, URL or branch that starts with `-` is refused, and `--` goes before the URL and refspecs. After a push the remote-tracking ref is set to the sha sent; after a pull it is set from `FETCH_HEAD`, if it exists, so ahead and behind stay right.

What the pinned run does not run: the repository's hooks (`core.hooksPath` is pointed at nothing, so `post-merge` and the rest do not run), and the `ext::` transport. The card says "Hooks are not run for a push from the phone" (or pull). A push is refused, with the reason "this repository has a pre-push hook or Git LFS: push from the desk", when the repository has a `pre-push` hook (in `.git/hooks`, or wherever `core.hooksPath` puts it), a `core.hooksPath` of its own, an `filter.lfs.*` setting, or `filter=lfs` in the top-level `.gitattributes` or `.git/info/attributes`: the owner's check would be skipped and LFS would send pointers without their content. Attributes files in subfolders are not read.

What it still runs: programs from the user's own system or global configuration (their ssh command, credential helper, proxy). A repository whose own (local, worktree or command-line) configuration sets any of these is refused rather than overridden, because a blank value would also switch off the user's own: `core.sshCommand`, `core.askPass`, `core.gitProxy`, `core.alternateRefsCommand`, a credential helper, `remote.<name>.uploadpack`, `receivepack`, `vcs` or `proxy`, `protocol.ext.allow` other than `never`, `ssh.variant`, any `url.<base>.insteadOf` or `pushInsteadOf`, any `http.proxy`, `http.curloptResolve`, `http.sslCAInfo`, `http.sslCAPath` or `http.sslVerify` (also the `http.<url>.*` forms), a `submodule.<name>.update` that starts with `!` (for a pull), and (for a pull) a filter driver. A repository with a local `credential.helper`, `core.sshCommand` or `http.proxy` is therefore refused for phone git: push or pull from the desk. The reason is shown on the desk and sent to the phone.

`url.<base>.insteadOf` and `pushInsteadOf` are refused because git applies them once to get the pinned URL and again to a URL given on the command line, so a chain of two rules shows one host and connects to another. As a second check, `git ls-remote --get-url <pinned URL>` must return the pinned URL unchanged, at the ask and again immediately before git starts; a user's own global rules that chain are refused by it too. The pinned pull does not use `--recurse-submodules`. `push.recurseSubmodules=check` overrides a user's own setting for the pinned push.

A pull is pinned whenever it is asked, whether or not the repository has a push remote, and is refused, with a reason, when `branch.<b>.remote` is a URL or `.` rather than a named remote, when it or `branch.<b>.merge` has more than one value, or when the remote has no `url`. Git would merge every `merge` value; the pin holds only one, so it fails closed. A branch whose upstream has another name than the branch is refused for a push. A push setting that cannot be read is treated as one that widens the push.

1. The phone sends `commit`, `gitPush` or `gitPull` with a `path` in a known project that is a working tree. A commit needs a message of up to 1,000 characters. `ghPRComment` and `ghIssueComment` need a PR or issue number and a body of up to 1,000 characters. A message or comment that the desk's redaction would change, or that holds a control or formatting character, is refused (`bad`), not posted as sent. A `bad` answer carries a `reason` in fixed words (the text is empty, too long, would be changed by redaction, holds a control character; the folder is not a known project; no changed files, or too many; what a push would send could not be read; a plain push would send more than the current branch). The reason never quotes the request or the repository.
2. The phone is sent `gitConfirm` with a state: `waiting`, then `allowed`, `denied`, `expired`, or `off` (the right went, the key changed or the socket closed), or `moved` (the folder, branch, remote or files changed between the question and the yes). `wait` (the desk said no lately; `waitSecs` is how long until a question is put), `busy` (another question of this device is open, or the device asks too fast), `nodesk` (no window on this machine to ask) and `bad` (with `reason`) come at once.
3. Every window on this machine that opened as a page is shown a card: the device name, the full folder, the branch, for a push or pull the remote, for a commit every changed file the host finds (the first 25 by name, then a count, and how many of the names not shown in full look like secrets: `.env`, `.pem`, `.key`, `id_rsa` and similar) and the whole message, for a comment the number and the whole comment. The host reads the repository itself at that moment; what the phone says about files is ignored. Each window has its own nonce.
4. The answer, `deskConfirmAnswer`, is taken only from such a window with its nonce, and only once. The device's right is checked again before anything runs.
For a push, and a commit with push, the card also gives the commit the branch is at (the first 12 characters of its hash) and how many commits the push sends (a commit with push counts the commit it makes). A push is refused (`bad`) at the ask if `push.default`, a `remote.<name>.push` refspec, a mirror remote, `branch.<name>.pushRemote` or `remote.pushDefault` would send more than the current branch or send it elsewhere.
5. A yes first checks that the folder is still a known project, the branch and remote are the same, the branch is at the same commit, a push would send the same number of commits under the same settings and, for a commit, the files and their sizes and times are the ones shown; if not, the device gets `moved`, the desk is told in a notice that nothing ran, and nothing runs. The same notice is sent if the device lost its right or its key between the question and the yes. Otherwise it runs that one request in the folder shown, with the message the device sent and the files the host listed, as a remote client, so a git settings or hook warning is still answered at the desk. Nothing is remembered; the next request is asked again.

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

The Go tests in `internal/server/sealed*_test.go` run a real handshake against a fake device. The phone client's tests run against a fake desktop. Nothing automated runs a phone through the real relay, so push, the handshake through the real relay, the relay's 4 MiB buffer and 4,096-frame limits, and the desk card on a real screen are checked by hand: see [remote-phone-checklist.md](remote-phone-checklist.md).
