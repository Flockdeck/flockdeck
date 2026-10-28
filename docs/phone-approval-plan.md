# Phone approval plan

Written on 28 Oct 2026 against origin/main 0f97bdee (through #731). Design only; no code was changed or run for this page. The decision behind it is the user's: before structured panes become the default, a phone has to get a push when a structured pane waits for approval, and has to be able to allow or deny from the phone. This is item 1 of "What is still open" in the [parity audit](acp-parity-audit.md).

What I read: `internal/server/push.go`, `relayscrub.go`, `viewerrole.go`, `permission_answer.go`, `structured.go`, `structuredrewind.go`, `artifacts_socket.go`, `artifacts_consent.go`, `artifacts_audit.go`, `internal/e2e/e2e.go`, `internal/remote/webpush.go`, `internal/store/deviceaccess.go`, `docs/structured-pane.md`, `ui/help/pages/remote.md`, `api/README.md`, and the card code in `internal/acp/bridge` and `claudeacp/perm.go`. Not available here: the relay (`flockdeck-relay`) and the phone app (`flockdeck-remote`). Everything this page says about them comes from comments and docs in this repo, and is marked where it matters.

## Decisions

Made on 28 Oct 2026, after the first version of this page. The user decided 1 to 4; the project manager (flockdeck-70) decided the rest. Where the text below was written before them, it has been changed to match.

| # | Question | Decision |
| --- | --- | --- |
| 1 | Transport | The sealed `/ws/approvals` socket, and a device must have a verified key before it can approve. No approval commands on the control socket. |
| 2 | Plans and edits | Commands and edits can be allowed. Plans, questions, clipped or redacted text, and agents on foreign adapters are Deny only, or desk only where there is nothing to deny (plans and questions). |
| 3 | Default | Off for every device, existing full devices included. Turned on per device at the desk. |
| 4 | Audit log and unanswerable waits | The log keeps a hash and the length of the shown text, not the text. A wait the phone cannot answer is still pushed, with "Answer on the desktop". |
| 5 | Agents | Allow only for the Claude and Codex adapters. opencode and Gemini: Deny only. |
| 6 | Where the UI goes | The phone app (`flockdeck-remote`) only. The desk-origin interface on a phone is unchanged and keeps saying "details on the desktop". |
| 7 | Numbers | 700 ms minimum display time and the rate limits (burst of 5, then 1 a second, three refusals close the socket) stay as starting values, to be tuned in the live check. |

One reading of decision 4 is mine and needs confirming: I take "a wait the phone cannot answer" to include a structured wait on a device that has approvals off, so every device that gets pushes at all is told of a structured wait, and only a device with approvals on and an answerable card is told "Allow or deny". The first version of this page pushed structured waits to approval-enabled devices only.

## Summary

Carry approvals over a new socket, `/ws/approvals`, that is end-to-end encrypted and mandatory-sealed like `/ws/artifacts`, not over the control socket. Send a push for a structured wait with fixed wording. The phone can answer allow once or deny on a command or an edit, on panes run by Flockdeck's own Claude and Codex adapters, and nothing else. It is off for every device until the user turns it on at the desk, for that device, after verifying its key.

The reason for the sealed socket is the one the docs already give for keeping API keys off the phone: whatever travels the control socket, the relay can read and could forge. An approval is useless unless the person sees what they approve, and what they would see is a command, which can hold a secret. So the card text has to travel somewhere the relay cannot read.

## What exists today

- A terminal pane that waits gets a Web Push to each device that asked for one (`pushDue`). The push is sealed on this machine for each device (RFC 8291); the relay posts it and cannot read it. It names the pane and project, or only a count if Anonymous is set. A tap opens the pane on the phone, where the terminal shows the whole prompt and the answer is typed over the end-to-end encrypted terminal socket.
- A structured pane is skipped by `pushDue` (`p.Sess == nil`, #378). On a phone it shows as waiting with its name, and nothing else (`scrubPane` clears the permission card, the last reply and the error, sets `detailsOnDesktop`).
- `permissionAnswer` exists and is atomic: it checks the card's `seq` and `requestId` against the pending card and takes the first answer. The relay refusal is one `if c.remote` marked `permission-answer-relay`. Every `structured*` command is refused to relay windows in `structuredCommand`, and `commandAccess` marks them `accessFull`, so a viewer cannot send them either.
- The control socket is not end-to-end encrypted. Only terminals and `/ws/artifacts` are. `remote.md` says so: everything else "passes through the relay decrypted, so it is trusted", and the relay operator can already open a window as any paired device and open a shell.
- `/ws/artifacts` is the model to copy. It refuses to run without a key on both sides, requires the desk to have switched the kind on and acknowledged a wording version, requires the device on an allowlist with a verified key, rechecks all of it on every request from the preferences file, rate-limits per device, closes on protocol abuse, and writes an audit line for each connection, refusal and desk change.

## Threat model

Parties and what each can do today, then what changes.

| Party | Today | With this design |
| --- | --- | --- |
| Relay operator (passive) | Reads pane roster, names, status, branch, diffs, anything typed in dialogs, and unsealed terminals. Cannot read sealed terminals or push text. | Learns that a device opened an approvals socket, when, and roughly how large each frame is. Reads no card text and no decision. Sees pushes exactly as today. |
| Relay operator (active) | Can serve altered page code, and can open a window as any paired device and use it, including a shell. A malicious key hand-out is caught only if the user pressed Verify. | Cannot inject a decision without the device's key, if the device is verified. Altered page code on the phone could still use the device's non-extractable key to approve. That is the limit `remote.md` already states for terminals; the mitigation is the same (run your own relay). |
| Stolen, unlocked, full phone | Full access: type to every agent, open a shell. Cannot answer a structured permission. | Can allow what is waiting, but could already open a shell and do more. The new risk is small for a full device. |
| Stolen phone, approve flag on, viewer role | Sees nothing without watch-panes. | Can allow or deny what an agent already asked for, and cannot choose the command. An allowed command runs on this machine. This is the case the per-device switch and the unpair path exist for. |
| Program on the desk machine that can read the desk window's own traffic (an agent that reaches the window token, or a local process) | Can already send any control command the window can. | Can open its own page connection, ask for the one-time challenge and turn approval on for a verified device. The challenge ties a yes to a connection that asked, to the device and to the wording, once; it does not show that a person is looking. Closing this needs a confirmation the program cannot reach (an OS-level prompt), which is not built. Anyone who can do this can already type to every agent on the machine. |
| Prompt-injected agent | Asks for a harmful command, hopes the person allows it. | Same, and the person is now more likely to be on a small screen, in a hurry. This is the main risk, and it is a UI one: see "What the phone shows". |
| Hostile or buggy agent over an adapter Flockdeck did not write (opencode, Gemini) | Agent picks its own option ids and kinds; Flockdeck does not interpret them. | The phone is not offered Allow for these. An agent could label an always-allow option "allow_once"; Flockdeck cannot tell. Deny only. |

Today's rules that stay: API keys and secrets are set at the desk only because a key typed on a phone passes through the relay in the clear (`remote.md`, "Done only at the desk"); structured data crosses the control socket never; `permissionAnswer` stays refused on the control socket for relay windows, so there is one path for a phone and it is the sealed one; question forms stay desk only. Terminal panes and the control socket are unchanged.

## The design

### a. Push content

Fixed wording, built from two things only: the pane's label and, unless Anonymous is set, the project name. No tool name, no command, no file, no agent text.

| Case | Title | Body |
| --- | --- | --- |
| One structured pane waits and the phone can answer it | `NAME · PROJECT needs you` | `Allow or deny. On DESKTOP` |
| One structured pane waits and the phone cannot answer (plan, question, other tool kinds on an agent not ours) | `NAME · PROJECT needs you` | `Answer on the desktop. On DESKTOP` |
| Several panes wait | `N agents need you` | `On DESKTOP: NAME, NAME` (as today) |
| Anonymous | `An agent on DESKTOP needs you` | none (as today) |

Details:

- The pane label is `p.Name`, the one terminal pushes already use. `LabelAuto` labels are made from the task and must not be used; `scrubPaneNames` already treats them that way. `p.Name` is the name given at creation or the directory's base name, not the task (R6); it is capped and redacted like `scrubPaneNames` does.
- The wait is pushed on the existing timer and rules: the delay (30 s default), one push a minute, the desk-in-use check, `phoneLook`, per-pane mute. Nothing new there.
- Every device that gets pushes is told of a structured wait (decision 4). Only a device with approvals on, and a wait whose card the phone can answer, gets "Allow or deny"; every other case gets "Answer on the desktop". Two audiences means two bodies. `PushTo` seals one `Notification` for every device its filter allows, so T4 either calls it twice with two filters or changes it to take a function from device id to notification. See research item R2.
- No Approve and Deny buttons on the notification. A lock-screen tap would approve something the person has not seen. A tap opens the pane's page, `/d/HOST/PANE`, the only target the service worker takes (R7), and the pane page shows the card.

### b. What the phone can answer

One decision per card: allow once or deny. Never allow always, never a question card answer or free text, never a plan.

The phone sends `decision: "allow" | "deny"`, not an option id. The host maps it: it looks at the pending card, finds the option with id `allow` (kind `allow_once`) or `reject` (kind `reject_once`), and refuses if there is not exactly one of that id and kind. Both Flockdeck adapters use those ids (`claudeacp/perm.go`, `codexacp/perm.go`). The phone cannot name `allow_always`, and a card that offers it is still answerable by allow once.

Allow is offered only when all of these hold; otherwise the card shows with Deny only and a one-line reason.

1. The pane's agent runs on a Flockdeck-written adapter (Claude, Codex). Other agents: deny only. Unsure how the server learns the adapter from a `StructuredPane`; T2 adds it to the interface.
2. The card kind is `execute` or `edit`. Other tool kinds (fetch, MCP, delete, move, read, search, other): deny only. Delete and move could be added later once the path display has been looked at.
3. The text shown is the whole of what will run or change: the command is not clipped (the existing cut is 500 characters, `structuredCommandCut`), is at most 8 lines, and contains no character removed by cleaning. For an edit, the path is not clipped.
4. `record.Redact` changed nothing in it. If it did, the card shows the redacted text and offers Deny only. A command with a token in it should be judged at the desk.
5. The card has no questions and no plan body.

Plan cards (`ExitPlanMode`) are desk only, and deny is not offered for them either. I considered a bounded, cleaned plan. The plan is up to 32 KiB of markdown with links and images, and approving it authorises a whole run of edits, so a summary of its first screens is the wrong thing to approve on. The push for a plan wait says "Answer on the desktop". This can be revisited when the phone has a plan view that shows all of it.

Edits are approved without the diff. The phone shows the path, and the number of lines added and removed. The diff is up to 64 KiB and is the largest thing a card holds. This is a judgement call, listed below.

### c. The wire change

API 1.26. Two places change.

On the control socket (relay-readable, so nothing sensitive):

| Item | Notes |
| --- | --- |
| `setDeviceApprove` command | `deviceId`, `on`. `accessFull`, refused to any relay window like `setDeviceRole`. Turning it on needs the device's key verified, and records the acknowledgement version. Ending it closes that device's approvals sockets. |
| `approve` field per device in `remoteDevices`, and in the catalog | Whether approvals are on, and whether the key is verified. |
| `setApprovalsOff` command | Global kill switch, desk only, also as `flockdeck remote approvals off` so it works headless. Same shape as the artifacts switch. |
| `canApprove` in `hello` | So a phone knows whether to open the socket. |

On `/ws/approvals`, a new socket that exists only for relay requests (404 on the local listener, as `/ws/artifacts` is) and closes without a sealed session:

| Frame | Direction | Content |
| --- | --- | --- |
| `approvalList` | phone to host | Ask for what is waiting. Also sent by the host unprompted when a card appears, changes or ends. |
| `approvals` | host to phone | For each waiting pane that this device may see: `pane`, `label`, `project`, `token`, `cardSeq`, `requestId`, `offer`, `kind` (`execute` or `edit`), `text`, `lines` (for edits, added and removed), `canAllow`, `why` (fixed reason text when not), `since`. |
| `approvalDecide` | phone to host | `pane`, `token`, `cardSeq`, `requestId`, `offer`, `decision`. |
| `approvalResult` | host to phone | `ok` or a fixed refusal text; the same texts as `permissionAnswer` where they exist. |

Replay and stale protection, in layers:

- The channel. Sealed frames carry a counter that must go up by exactly one (`internal/e2e`), so a captured frame cannot be replayed on the same socket, and the session keys are per-socket, so it cannot be replayed on another.
- `offer`. A random 128-bit value the host makes for each card it shows on this socket. A decision names an offer the host made on this socket for the card now pending; it is used once. A decision with an unknown or used offer counts toward the abuse limit, as `artifactUnknownMax` does.
- `token`. The pane runtime's identity, `Runtime.RewindToken()` (`seq.epoch`), as `structuredRewind` uses it. The card `seq` is already unique across panes and restarts within one run of Flockdeck (a process-wide counter with a random tag in the high bits, `nextCardSeq`), so the token is a second lock and not the only one. It changes when the pane restarts (a restart makes a new `Runtime` with a new `seq`) and when the conversation is rewound. See research item R4.
- `cardSeq` and `requestId`, checked inside the existing atomic `AnswerPermission`, so two answers (phone and desk, or two phones) cannot both win.
- A decision arriving less than 700 ms after the card was sent to that socket is refused. This stops a stray tap on a card that appeared under the thumb. Unsure it is worth a rule; it is cheap.
- Rate: per device, a burst of 5 decisions and then 1 a second; 3 refusals in a row close the socket and put a notice on the desk. The numbers are a guess and want the live check.
- No queueing. If the socket is down when the person taps, the phone says so and the tap does nothing.

Roles. Approval is a separate switch per device and is off for everyone by default, including existing full devices: the "no record means full" compatibility rule is not extended to it. It works for either role. A viewer with the switch on can see the cards sent to it and decide them, and still cannot type, resize or open panes. A full device is not allowed to approve just for being full. `mayPush` stays as it is for terminal waits; a structured wait goes to a device only if it has approvals on.

The desk's part: turning a device on shows fixed text (a wording version, as `artifactAckVersion`): "This device will be shown the commands and file paths your agents ask to run or change, and can allow or deny them. The relay cannot read them." It requires the verified-key mark. It is checked again on every frame from the preferences file, so a switch turned off takes effect on the next frame.

### d. What the relay carries and learns

Carries: one more WebSocket path per device, like `/ws/artifacts`. Whether the relay routes by an allowlist of paths is something I could not check from here. If it does, the relay needs the new path added; if it proxies any `/ws/*`, it needs nothing. Pushes need no relay change: the sealed per-device messages are already opaque to it.

Learns, beyond today: that a device used the approvals socket, and when; the size of each sealed frame; and the timing of frames, which shows roughly when a decision was made. It does not learn which pane, what was asked, or what was answered. Frame size would give away roughly how long a command is. Padding closes that, and it needs no change to the `e2e` frame format: `Session.Seal` takes any plaintext, so the application pads inside it (a JSON field of filler up to a 1,024-byte bucket, as the push path does with `pushRecord`) and strips it on the way in. See research item R3.

The control-socket alternative would have the relay learn the pane, the card, the tool and the decision, and let it forge one. That is the difference that matters.

Does it learn less than for a terminal pane today? About the same. A terminal prompt answered on the phone is also sealed, and the relay sees timing and size.

### e. Audit log

A new file, `remote-approvals.log`, in the state folder, using the artifacts audit machinery (`auditLog`, 1 MiB, three files, owner-only). One JSON line for: socket opened, socket refused (with reason), card shown, decision made, decision refused (with reason), and each desk change (approvals on or off for a device, global switch). Fields: time, event, device id, device name, pane id, pane name (cleaned), card seq, decision (`allow_once` or `reject_once`), tool kind, the length of the text, and a SHA-256 of the text that was shown.

It does not store the command or path. Redaction is best-effort, and a log that keeps commands keeps whatever it missed. The hash lets someone with the conversation record check that the card they see is the one that was approved. Unsure whether the conversation record holds the card text and the outcome; if it does not, the hash proves less than I would like, and the user may want to revisit decision 4. See research item R5.

The desk side also gets a visible trace: the card on the desk, if it is open, changes to "Allowed from PHONE NAME at TIME" and a notice appears. How much of this the bridge's conversation entries support is open (R5); T5.

The existing desk answer logs to stderr only (`permissionLog`). It is not part of this work, but a phone approval log with no desk equivalent looks lopsided. Worth a line in the review.

### f. UI on the phone

The approvals page (an extension of the phone app's pane list; the app is in `flockdeck-remote`, not seen here):

- A pane that has a waiting card shows "Needs approval" and opens the card. The push's tap goes straight to it.
- The card: pane label and project; a heading from the kind ("Run a command" or "Change a file"); the text in monospace, whole, scrollable, never truncated with an ellipsis; for an edit, the path and "+12 −3 lines"; how long it has waited.
- Two large buttons, far apart: Deny and Allow once. Deny is the default focus. Each has the pane label in its accessible name.
- When Allow is not offered, the Allow button is absent and the reason is one fixed sentence: "Too long to check here. Deny, or answer on the desktop." or "Plan approval is on the desktop."
- After a tap: "Sent", then the card disappears when the host confirms. A refusal shows the host's fixed text. A stale card disappears with "Already answered".
- Text from the agent is drawn as text, never as markdown or HTML, with control and bidirectional characters removed on the host.
- Offline: buttons disabled with "Not connected". No retries on the user's behalf.
- Settings: on the phone, a line saying whether approvals are on for this device and "turn on at your desktop". It cannot turn itself on.

The full interface reached through the relay (the desk-origin UI in `ui/`) is not changed (decision 6): its cards keep saying "details on the desktop" for a relay window.

## Tests and live check

Unit and server tests, in this repo:

- Card projection: a table of cards (command, long command, multi-line, command with a secret, bidi override, control characters, edit with a long path, plan, question, MCP, an agent with `allow_always` only, two `allow_once` options) against the expected `canAllow` and `why`. Plus a fuzz test that the output never contains a character outside the allowed set and never exceeds the cut.
- Decision mapping: `allow` maps to id `allow` only; refused if the card has no such option, two of them, or a `kind` that does not match.
- Socket gate: refused without a key on either side, without a verified key, with the switch off, with the device off, with an old acknowledgement version; and each of these rechecked after the socket is open.
- Replay and staleness: a used offer, an offer from another socket, a wrong token, a stale `cardSeq`, a reused `requestId` on a new card, a pane restarted between show and decide, two devices deciding at once, a desk answer racing a phone answer. Each must change nothing and give the fixed text.
- Rate: burst, refill, three refusals closing the socket.
- Scrub tests: `TestEveryCommandHasAnAccessDecision`, `TestEveryMessageTypeIsClassifiedForTheRelay` and the API catalog test keep passing; add a test that no card text appears in any control-socket message to a relay window (the existing `TestARelayWindowGetsNothingOfAStructuredPanesConversation` is the pattern).
- Push: a structured wait pushes only to devices with approvals on; the title and body match the table exactly; Anonymous says only the count; a plan wait says "Answer on the desktop"; the push never contains the command (assert on a command with a marker string).
- Audit: one line per event, no card text, rotated at the size.
- A negative test that `permissionAnswer` is still refused to a relay window.

Live check, by hand, with a real phone and a real relay (not the shared production relay at first). Nothing here has been run.

1. Pair a phone, verify its key at the desk, turn approvals on for it. Check the desk text and the log line.
2. Start a Claude structured pane and ask for a harmless command. Lock the phone. Check the push arrives, reads as the table says, and shows nothing else on the lock screen.
3. Tap it. Check the card text equals the command. Allow once. Check the agent runs it and the desk shows "Allowed from PHONE".
4. Deny a second one. Check the agent reports a refusal.
5. Ask for a command over 500 characters, one with a fake token in it, and one in a plan. Check Allow is absent for each, with the reason.
6. Answer on the desk while the phone shows the card. Check the phone's tap is refused as stale.
7. Restart the pane while the phone shows the card. Check the tap is refused.
8. Turn the device off at the desk with the card open. Check the socket closes.
9. Watch what the relay sees: run the relay with request logging on and confirm no card text, pane id or decision is in anything it logs. This is the check that matters most and needs the relay's repo and someone who can read its logs.
10. Run the same on a phone with the key not verified; it must be refused.
11. Isolate the test instance from the real config dir first (see the notes on test isolation on Windows).

## Research list

What the first version of this page could not answer, checked on 28 Oct 2026 against this repo and the other checkouts on this machine. Under `C:\Users\jmwri\Documents\repos` there is no `flockdeck-relay`. There are two checkouts of `flockdeck-remote` (the phone app): `flockdeck-remote-fix-review2` (branch `fix/streaming-reply-and-detail-timeout`, c21e83b, 22 Sep 2026) and `flockdeck-remote-review-2` (a762a68, 21 Sep 2026). Both are review copies and a month behind. They were read only. Anything about the relay below is inferred from the desktop's side of it.

| # | Question | Answer | Still needs |
| --- | --- | --- | --- |
| R1 | Does the relay carry `/ws/approvals`, or does it allowlist paths? | Not answerable here. The desktop trusts the headers the relay sets (`Flockdeck-Remote-Device`, `-Device-Name`, `-Origin`, and `fromRemote`) and answers a request without them with 404 or 403. The phone app opens `/h/HOST/ws/control` and `/h/HOST/ws/pty`; it has no client for `/ws/artifacts` in either checkout, so nothing on this machine shows the relay passing that path. | The relay repo, which is not on this machine. Ask whoever has it: does it proxy any `/h/HOST/ws/*`, and does it log paths, frame sizes or timing? Affects T7 and live check step 9. |
| R2 | Can `PushTo` send different content to different devices? | Not as it is. `PushTo(ctx, n, allow)` seals one `Notification` for each device `allow` says yes to. The relay call (`POST /api/v1/host/notify`) already takes one sealed message per device, so the relay is not the limit. Two audiences can be done with two `PushTo` calls (two device lookups, two posts) or by changing `PushTo` to take `func(deviceID) (Notification, bool)`. The second is cleaner and is a desktop-only change. `maxPushDevices` (32) applies per call. | Nothing from other repos. Decided in T4. |
| R3 | Can the e2e frames be padded? | Yes, in the application. A frame is a version byte, an 8-byte counter and AES-GCM over the plaintext (`Session.Seal` and `Open` in `internal/e2e`); the plaintext is whatever the caller passes. Padding is a filler field inside the JSON, stripped on receipt. The phone app's `e2e.js` is a port of the same scheme, so the phone strips it the same way. No format change on either side. | Nothing. |
| R4 | Does `RewindToken` change when a pane restarts? | Yes. It is `"<runtime seq>.<epoch>"`. A restart makes a new `Runtime` through `structured.Start` (`internal/workspace/structuredpane.go:124`), which takes the next number from a process-wide counter, and a rewind raises the epoch. Also, the card `seq` is not per bridge as the first version said: `nextCardSeq` is one process-wide counter with a random tag in the high bits, so card seqs are unique across panes and restarts. Neither survives a restart of Flockdeck itself, but then every socket is gone too. | Nothing. |
| R5 | Does the conversation record keep a permission card and its outcome? | Not found in the structured path I read: `bridge` keeps the card as the pending one and closes it (`closeCard`), and `permissionLog` writes the answer to stderr. I did not find a conversation entry for the card or its answer. If there is none, the audit hash cannot be checked against anything later. | A read of `internal/acp/bridge` entries and `internal/record` by whoever builds T5, before the log line's format is fixed. Decision 4 stands; this is its cost. |
| R6 | Is `p.Name` derived from the task? | No. `newPane` sets `Name` to the name it was given or, if empty, `filepath.Base(cwd)`. The task-derived value is `Label` with `LabelAuto`, which the push does not use. `Name` can still hold something sensitive (a directory named after a customer), so the push uses the same `capName` and redaction as `scrubPaneNames`. | Nothing. |
| R7 | Can a push tap reach the approvals page? | The service worker takes only `/d/HOST` or `/d/HOST/PANE` as a tap target (`tapURL` in `web/app/sw.js`) and shows no notification actions. `/d/HOST/PANE?approve`, as this page said earlier, would be rejected by that pattern. The tap goes to the pane page, and the pane page shows the card. `answer.js` already draws Yes and No for terminal panes; the approvals card is a separate piece beside it. | Phone app change, T6. |
| R8 | Can the phone app run a second sealed socket with its device key? | `e2e.js` in the phone app and `ui/src/e2e.js` here both say the long-term key is a non-extractable WebCrypto key in IndexedDB, and a handshake runs per socket, as the terminal does. I did not read the phone app's handshake code closely. | T6 author reads `web/app/e2e.js` and `terminal.js` first. |

## Staged tasks

Sizes: S under half a day, M about a day, L two or more days. "Needs" means the task cannot be merged before the one it names.

T1 and T2 share no files and can start the same day, in two worktrees.

| # | Task | Size | Needs | API minor |
| --- | --- | --- | --- | --- |
| T1 | Prefs and desk switch: a per-device `approve` flag in `store.DeviceAccess` (off when absent, so existing full devices are off), the acknowledgement version, `setDeviceApprove` and `setApprovalsOff` (`accessFull`, refused to relay windows), the verified-key requirement when turning one on, `flockdeck remote approvals on\|off\|status`, the fields in `remoteDevices` and `hello`, the entries in the access table and `api/catalog.json`, the desk dialog. A reader `mayApprove(deviceID)` that T3 and T4 call. | M | none | 1.26 |
| T2 | Card projection and decision mapping: a pure function from a pending card and the pane's adapter to `{kind, text, lines, canAllow, why}`, the `allow` and `reject` mapping, the token, the redaction and clip rules, bidi and control character stripping, adapter identity added to `StructuredPane`. Table and fuzz tests. No socket, no prefs. | M | none | none |
| T3 | `/ws/approvals`: handshake reuse, the gate (key on both sides, verified, `mayApprove`, global switch, ack version, rechecked per frame), `approvalList`, `approvals`, `approvalDecide`, `approvalResult`, offers, rate limits, abuse closing, padding, the audit log file and its refusal lines. | L | T1, T2 | none if the sealed frames stay out of the catalog; 1.27 if a `sockets` section is added to it |
| T4 | Push for structured waits: lift the `p.Sess == nil` skip, two audiences, the wordings, plan and question variants, `PushTo` changed per R2, tests on wording and on the absence of card text. | M | T1, T2 | none |
| T5 | Desk trace: "Allowed from PHONE" on the desk card and a notice, the decision lines in the log. Depends on R5. | S | T3 | a later minor: a new field on the `structured` message |
| T6 | Phone app (`flockdeck-remote`): approvals page and card, sealed socket client with the padding strip, offline and stale states, the service worker accepting the tap target (R7), tests. A different repo and release; the size is a guess. | M | T3's frame shapes (this page gives them, so it can be coded before T3 merges) | none |
| T7 | Relay: carry `/ws/approvals` if it allowlists paths (R1). May be nothing. | S | T3 | none |
| T8 | Docs: `structured-pane.md`, `remote.md` (what the relay can see, the switch), parity audit and matrix, the help page. Each says the opposite today. | S | T3, T4 | none |
| T9 | Live check, steps 1 to 11, written up, with the numbers tuned (decision 7). | S | T3, T4, T6, T7 | none |

Order that works: T1 and T2 in parallel; then T3 and T4 in parallel (both use T1's `mayApprove` and T2's projection, in different files); then T5 to T8; T9 last. T4 must not reach a release before T3: a push that sends the user to a page where they cannot answer is the reason #378 skipped structured panes.

API minors: 1.26 is T1's. A later task may need a bump: T5 for sure, T3 if the catalog describes sealed frames. Minors are taken at merge time by whoever merges second, so two branches do not both claim 1.27. `TestAPICatalogIsCurrent` fails if the catalog file and the tables disagree.

A smaller first cut, if the schedule needs one: T1 to T4 with `execute` cards only, edits following.

## T1 as built

T1 (the switch, no socket) was built on 28 Oct 2026 against origin/main e6ed68cd, as API 1.26 with the cap `deviceApprove`. Where it differs from the text above:

- The device is named `id`, not `deviceId`, as in `setDeviceRole`. `setDeviceApprove` takes `id`, `on` and `confirmed`. Turning it on needs a window that opened as a page and `confirmed: true` (the dialog has just shown the fixed wording), as `setRemoteArtifacts` does. Turning it off needs neither.
- The global kill switch is not a second flag. `setApprovalsOff` and `flockdeck remote approvals off` clear `approve` and the agreement on every device and keep roles, like `artifacts off`. A device is allowed again one at a time. The gate in T3 therefore has two inputs, not three: the device record and the key.
- `flockdeck remote approvals` has `off` and `status`. `on` is refused with a message; the plan listed `on|off|status`, but enabling is desk only.
- The record is in `store.DeviceAccess`: `approve`, `approveV` (wording version) and `approveAt`. A full device with `approve` is kept in the map. Changing a role keeps the setting; unpairing drops it. The "no record means full" rule does not reach it.
- `mayApproveWithKey(deviceID, key)` is the gate, and the only one T3 and T4 call. It is true only when the running program's record and the preferences file both say yes at the current wording, and the grant was made for this key (`approveKey` is a SHA-256 of the device's usual end-to-end key at the time). The plan said "from the preferences file" for every frame; the running program's record is the one in force for roles, so the file can only take approval away. An unreadable or damaged file says no. `approveSwitchOn(deviceID)` is the same without the key, for telling a device in `hello` and for the desk's lists; T4's push audience can use it, but a decision must not.
- The grant is tied to the key. Unverifying the key, verifying a different one, a key that changed, a device paired again under the same id, and a record restored from an old file all end the grant; the desk has to turn approval on again. A record from before `approveKey` existed has no key and says no.
- A disable always wins over a turn-on that is still being checked. Turning on verifies the key over the network, off the workspace goroutine. A counter (`approveGen`) is bumped by turning a device off, by the kill switch, and by unpairing or a device missing from the roster; the turn-on reads it before it starts and is dropped inside the change closure if it moved. The roster is not asked again at apply time (it needs the network); a device removed meanwhile is caught by the bump.
- Turning on carries a one-time challenge. The dialog sends `approveChallenge` (`id`) when it opens and gets `approveChallenge` back with `nonce`, `v` (the wording version) and `text`. `setDeviceApprove` with `on` must send `nonce`, `v` and `confirmed`. The nonce is bound to the connection that asked, the device and the wording version, is used once (spent by any attempt), expires after two minutes, and at most 16 are open. `approveChallenge` is desk only, page only, and refused to relay windows. A `v` that is not the current version is refused with a notice, so a dialog that showed older wording cannot turn approval on. The dialog also closes if the device is unpaired or its key changes while it is open.
- The preferences file is read for the gate by modification time and size, and again at least every 5 seconds. It is read without moving a damaged file aside (`store.PeekDeviceAccess`); the gate never changes the file. A file that cannot be read says no.
- The preferences are written back with the file's own say on approval: an unrelated save by the running program (`AdoptAccess`) does not write a grant back that `flockdeck remote approvals off` removed. The CLI sends the stop to a running instance even when it cannot read the file, and then reports the read error.
- `flockdeck remote approvals status` lists the devices the file allows at the current wording.
- The wording is version 2. It says that a relay that only passes traffic along cannot read the cards, and that a tampered relay, or a tampered page on the phone, could. Every grant at version 1 says no.
- A program that holds the window token can use up the 16 open challenges and so keep the desk's approve dialog from getting one ("Too many questions are open"). The dialog shows the host's error and keeps Allow approving off. Accepted: the same program can already send any control command.
- For T3: the key passed to `mayApproveWithKey` must be the one the sealed handshake proved for the connection, never one read from the roster or the request. T3 needs a test that calls it with another device's roster key and expects no.
- `hello.canApprove` is the switch, not the gate: a relay control window has no proven key at that point, and fetching one would make `hello` wait on the network. The roster refresh clears a grant whose key changed.
- Remaining limits. The challenge does not prove a person is looking (see the threat model: a program that can read the desk window's traffic can ask for one). The key binding does not stop a relay that serves altered page code from using the device's own key. The gate reads the file cache for up to 5 seconds when the file is replaced with the same size and timestamp; the record in memory still has to say yes, and the stop endpoint and the sockets cut do not depend on the file.
- The key checked when turning on is the device's usual key (`remote.KeyOriginUsual`), the one the phone app uses, not the desk-origin key the artifacts allowlist checks.
- Turning on is applied only if the preferences file could be saved. Turning off is applied in memory even if it cannot be, as a restriction is.
- `remoteDevices` (desk windows only) carries `approve` and `approveKey`. `approveKey` is the verify state of the usual key in the words of `verify`; a key that changed after approval was turned on shows as changed and approval is off (the grant ended with the key). `hello` carries `canApprove` for a window reached through the relay, true only when the device may approve.
- The CLI reaches a running instance with `POST /remote/approvals/stop` (URL token, never remote), because the running program's record wins over the file.
- Not done in T1: the audit log lines for desk changes (T3 owns `remote-approvals.log`), the help page text (T8), and a JSON schema entry: `hello` and `remoteDevices` have no schema in `api/schema`.

## T2 as built

Written on 28 Oct 2026. The card projection, the decision mapping and the stale-answer lock are in `internal/server/approval_card.go`. They are pure functions with no socket, no preferences and no API change. Where the code disagreed with this page, the safer reading was taken; each case is listed.

What it does:

- `projectCard(card, own)` returns what a phone may see (`Answerable`, `Kind`, `Text`, `Added`, `Removed`, `CanAllow`, `Why`). `Why` is one of ten fixed sentences. Nothing else of the agent's reaches the phone.
- `phoneAnswer(card, token, own, lock, decision)` takes `allow` or `deny`, checks the lock, re-derives the projection from the card as it is now, and returns the shown id of the one option to give `AnswerPermission`. Any other decision, `allow_always` included, is refused.
- `approvalLock{Seq, RequestID, Token}` is made by `lockFor` when a card is shown and compared by `check` when a decision arrives. A part that is zero or empty never matches.
- `StructuredPane` has two new methods, `OwnAdapter()` and `RuntimeToken()`. The first is `manifest.OwnAdapter` (the exact shapes of the `claude` and `codex-go` entries, the predicate `OwnRewind` already used) asked through `Spec.ACPOwnAdapter()` and `Runtime.OwnAdapter()`. A runtime started with a test command is not ours. The second is `Runtime.RewindToken()`.

Where the code disagreed with the plan:

1. A card's `Command` is the first line of the command only (`bridge.line` keeps one line and cuts at 4,096 characters). `echo ok` followed by a line break and `rm -rf ~` shows as `echo ok`, on the desk as well as on a phone. The eight-line rule could not have been met from it. `bridge.Card` now also has `CommandText`, the cleaned command with its line breaks (cut at 4 KiB), and `CommandAltered`, set when cleaning removed a character or the cut was made. The projection reads only these. A card with no `CommandText` is Deny only. The desk's `structuredCard.Command` is unchanged and still shows one line; that is a separate problem, listed in the report for T2.
2. "At most 8 lines" is joined by a byte limit: 500 bytes for a command (the desk's own cut, `structuredCommandCut`) and 300 for a path (`structuredCardCut`), so a command the phone may allow is one the desk shows whole.
3. Characters. Beyond controls and bidi marks, the projection refuses every space except the ASCII space, tab and line break (a no-break space looks like the space between arguments and is not one), all format characters, variation selectors, the blank fillers, unassigned and private characters, U+FFFD and U+FFFC. A carriage return is refused too, so a command with CRLF line ends is Deny only. The text shown has those characters removed, and the card is Deny only.
4. Plans are recognised by the ACP kind `switch_mode`, questions by their kind or by having questions. Both have `Answerable` false: nothing is offered, deny included. So is a card with no single `reject` option, since there would be nothing to deny with.
5. An execute card with no command (the Claude tools `BashOutput` and `KillShell` are kind `execute` and carry none) is Deny only. So is any execute or edit card with `Body` text, which is the reason Codex puts on a command or file change; that text is the agent's and is not shown.
6. An edit is allowed only when its diff reads as a diff of the card's path alone. The diff is read by the counts in each hunk header, so a changed line that begins `+++ ` is counted as a line. A diff that was cut, a note about more edits, a second path, or a hunk that does not hold what its header says makes the edit Deny only. A Codex file-change card has no path and no diff, and Claude's `NotebookEdit` has no diff, so those are Deny only.
7. Option matching is stricter than the page says. There must be exactly one option that has the id `allow` or the kind `allow_once`, and it must have both; the same for `reject` and `reject_once`. A decoy such as the id `allow` on an `allow_always` option next to a separate `allow_once` is refused.
8. Redaction runs on the text that is shown (the command, or the path) and not on the diff, which is not shown. The redacted text is what the phone sees when it is Deny only.

Known gap. Claude's `Write` reads the old file to make its diff and, if it cannot (the file is over the size it reads, or cannot be opened), draws the write as a new file. The count then says `+N −0` for a file that is being overwritten. The card cannot tell the two apart. If that matters, the adapter should say so on the card; until then the plan's "judgement call" on edits carries it.
