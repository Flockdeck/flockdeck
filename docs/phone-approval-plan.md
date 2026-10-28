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

Roles. Approval is a separate switch per device and is off for everyone by default, including existing full devices: the "no record means full" compatibility rule is not extended to it. It works for either role. A viewer with the switch on can see the cards sent to it and decide them, and still cannot type, resize or open panes. A full device is not allowed to approve just for being full. `mayPush` stays as it is for terminal waits. A structured wait goes to every device `mayPush` allows (decision 4, and "T4 as built"); only the wording differs, and "Allow or deny" goes to a device with approvals on and a card the phone may allow.

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
- Push: a structured wait pushes to every device `mayPush` allows, and only a device with approvals on and an allowable card is told "Allow or deny"; the title and body match the table exactly; Anonymous says only the count; a plan wait says "Answer on the desktop"; the push never contains the command (assert on a command with a marker string).
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

Written on 28 Oct 2026 and revised after the security review. The card projection, the decision mapping and the stale-answer lock are in `internal/server/approval_card.go`. They are pure functions with no socket and no preferences. The one API change is 1.27: the desk card shows the whole command and has two new optional fields (`cwd`, `notes`). Where the code disagreed with this page, the safer reading was taken; each case is listed.

What it does:

- `projectCard(card, own, root)` returns what a phone may see (`Answerable`, `Kind`, `Text`, `Added`, `Removed`, `CanAllow`, `Why`). `Why` is one of twelve fixed sentences. Nothing else of the agent's reaches the phone.
- `phoneAnswer(card, token, own, root, lock, decision)` takes `allow` or `deny`, checks the lock, re-derives the projection from the card as it is now, and returns the shown id of the one option to give `AnswerPermission`. Any other decision, `allow_always` included, is refused.
- `approvalLock{Seq, RequestID, Token}` is made by `lockFor` when a card is shown and compared by `check` when a decision arrives. A part that is zero or empty never matches.
- `StructuredPane` has three new methods: `OwnAdapter()`, `RuntimeToken()` and `ProjectRoot()`. `OwnAdapter` is `manifest.OwnAdapter` (the exact shapes of the `claude` and `codex-go` entries, the predicate `OwnRewind` already used) asked through `Spec.ACPOwnAdapter()` and `Runtime.OwnAdapter()`. A runtime started with a test command is not ours. `RuntimeToken` is `Runtime.RewindToken()`. `ProjectRoot` is the pane's absolute working directory.

Where the code disagreed with the plan:

1. A card's `Command` was the first line of the command only (`bridge.line` keeps one line). `echo ok` followed by a line break and `rm -rf ~` showed as `echo ok`, on the desk as well as on a phone. `bridge.Card` now has `CommandText`, the cleaned command with its line breaks (cut at 4 KiB), and `CommandAltered`, set when cleaning removed a character, the cut was made, the key `command` is repeated, or a key that differs from it only in case is present. `Command` is still the first line. The desk card (`structuredCard.Command`) now shows `CommandText`, cut at 4 KiB (`structuredCommandTextCut`), and `commandClipped` is set when it was cut or altered (the card-wide `clipped` is not); the desk draws "The command shown is not exactly what the agent sent." under it, with the line count ("N lines") and, for a command of more than 12 lines or 800 characters, "Scroll to read the whole command." The text is never changed to make it shorter. The projection reads only `CommandText`. A card with none is Deny only. `CommandText` and `CommandAltered` stay in memory: they are not fields of the pushed card and are not on any wire. The structured push is dropped for relay windows whole.
2. The command is read from the raw input with exact keys (`bridge/input.go`), not with Go's case-folding decoder, so `{"Command": ...}` is not taken for `command`.
3. Input the card does not show is noted, as fixed codes, not agent text. For a command, a key outside the list the adapter is known to send (`manifest.Adapter.ShownInputKeys`: `command`, `description`, `timeout` for the Claude adapter; `command`, `cwd` for the Codex adapter; nothing known for any other, so the built-in default list applies) is `extra_input`. `dangerouslyDisableSandbox`, `run_in_background` and `replace_all` with any value but false or null are `sandbox`, `background` and `replace_all`. Input that is not one JSON object is `extra_input`. A MultiEdit's `edits` are read element by element: `replace_all` in any element is `replace_all`, and a key outside `old_string`, `new_string` and `replace_all`, a repeated key or an element that is not an object is `extra_input`. A lone surrogate in the command (read as U+FFFD) sets `CommandAltered`, and in the directory adds `extra_input`. The keys of `BashOutput` and `KillShell` (`bash_id`, `filter`, `shell_id`) are in the Claude adapter's list, so those cards carry no spurious note. The desk shows one fixed sentence for each (`notes`) and the directory (`cwd`) when the adapter sends one. The phone is Deny only for a command with any note, and for a `cwd` that is not the pane's directory; an edit with a note is Deny only. The adapter route goes through the manifest and `Spec.ACPShownInput`, so no consumer branches on the agent type.
4. "At most 8 lines" is joined by a byte limit: 500 bytes for a command and 300 for a path. These are the phone's own and do not follow the desk's cut.
5. Characters. A command is allowed from a phone only when it is printable ASCII plus tab and line break (`plainASCII`); letters from another script that look Latin, combining marks and wide spaces make it Deny only. Beyond that, the cleaning removes controls, bidi marks, format characters (so U+FE00 to FE0E, U+E0101 to E01EF and U+1160 are all caught), blank fillers, unassigned and private characters, U+FFFD and U+FFFC, and the card is Deny only. A carriage return is refused, so CRLF line ends are Deny only. Eight or more spaces or tabs in a row, and more than two blank lines in a row, are Deny only. A path allows letters of one script per file stem, at most two combining marks in a row, and the same layout rules.
6. Plans are recognised by the ACP kind `switch_mode`, questions by their kind or by having questions. Both have `Answerable` false: nothing is offered, deny included. So is a card with no single `reject` option, since there would be nothing to deny with.
7. An execute card with no command (the Claude tools `BashOutput` and `KillShell` are kind `execute` and carry none) is Deny only. So is any execute or edit card with `Body` text, which is the reason Codex puts on a command or file change; that text is the agent's and is not shown.
8. An edit is allowed only when its diff reads as a diff of the card's path alone. The diff is read by the counts in each hunk header, so a changed line that begins `+++ ` is counted as a line. A diff that was cut, a note about more edits, a second path, or a hunk that does not hold what its header says makes the edit Deny only. A Codex file-change card has no path and no diff, and Claude's `NotebookEdit` has no diff, so those are Deny only.
9. An edit's path must be absolute, already clean (`filepath.Clean(p) == p`, so no `..` and no doubled separators), and below the pane's directory. No part of it below that directory may start with a dot (`.git`, `.claude`, `.github`, `.ssh`, `.vscode`, shell and tool configuration), end in a dot or a space, hold a colon or a tilde, or be a file name in `denyFiles` (build and test entry points such as `package.json`, `Makefile`, `Dockerfile`, `pyproject.toml`, `conftest.py`, and key files). The floor also refuses the Windows device names (`con`, `prn`, `aux`, `nul`, `com0` to `com9`, `lpt1` to `lpt9`, with or without an extension), files that are run by their extension or open a project in a tool (`.bat .cmd .ps1 .sh .vbs .lnk .reg .exe .dll .msi .jar .csproj .sln .props .targets .gradle .mk .cmake` and similar; `.js`, `.py` and `.go` are not on it), and more build files (`go.mod`, `go.work`, `CMakeLists.txt`, `Directory.Build.props`, `Jenkinsfile`, `docker-compose.yml`, `Procfile`, `gradlew` and similar). This list is a floor and not a promise: an ordinary source file runs when it is tested, so the lock to a project root limits where a phone-allowed edit lands, not what the edit does.
    At the moment of the decision, `phoneAnswer` also reads the file system for an edit: no part of the path below the root may be a symbolic link or an irregular entry (a Windows junction or other reparse point, which Go does not resolve), the root and the nearest existing folder are resolved with `filepath.EvalSymlinks`, and the resolved path is held to the same rules. Any error fails closed. A link that leads inside the project is refused too. A command is not checked this way.
10. Claude's `Write` reads the old file to make its diff. When it cannot (the file is over the size it reads, or cannot be opened), the card now carries a text block that says so instead of a diff, and the edit is Deny only. Before this it was drawn as a new file with `-0`.
11. Option matching is stricter than the page says. There must be exactly one option that has the id `allow` or the kind `allow_once`, and it must have both; the same for `reject` and `reject_once`. A decoy such as the id `allow` on an `allow_always` option next to a separate `allow_once` is refused.
12. Redaction runs on the text that is shown (the command, or the path) and not on the diff, which is not shown. The redacted text is what the phone sees when it is Deny only.

Codex: when an item was announced before its approval request, the card keeps the request's reason (why Codex asks, the network host, the root it also wants to write under) after the item's own content. The reason is `Body`, so the phone is Deny only and the desk shows it, and a network approval keeps its "Network access: " title.

The roster and graph show one line of a command; when the command has more lines it ends with " ..." (U+2026).

Known limits:

- Deny on a card from an adapter that is not ours answers with its reject option, the same as the desk does. There is no cancel path: `Runtime.answer` takes only the ids on the card, and an empty id is `ErrBadOption`. A foreign agent can therefore read a Deny as "reject once" and go on to ask again. Cancelling the turn instead would need a runtime method the desk does not have.
- The check of a path is pure. A link inside the project that points outside it is not seen.

## T4 as built

Written on 28 Oct 2026 against origin/main 3e8a5d06 (through #734). The push for a structured wait is in `internal/server/push.go` and is switched off: `structuredPushLive` is `false`. No API change.

What it does, once on:

- `pushDue` no longer skips a pane because it has no session. A pane with none is taken only if `structuredWait` finds it through the structured-pane lookup (`s.structuredPane`, the same one `permissionAnswer` uses, so no consumer asks which agent it is), and only while its status is waiting on a request. A structured pane that is blocked (needs setup) is not pushed, though a terminal pane that is blocked still is. The delay, the one push a minute, `deskInUse`, `phoneLook` and per-pane mute are the code they were.
- Wording is as the table in section a says. One structured pane waiting: title `NAME · PROJECT needs you`; body `Allow or deny. On DESKTOP` for a device with the approvals switch on when `projectCard(...).CanAllow` is true, and `Answer on the desktop. On DESKTOP` for every other device and for every other card (plan, question, Deny only, an agent that is not ours, no card). Several waiting panes and Anonymous are as before, the same for every device. `CanAllow` is the test and not `Answerable`: a Deny only card is told "Answer on the desktop", since "Allow or deny" would promise a button the card does not have. The card is read for that yes or no and nothing else; `waitingAgent` holds two booleans and no card text.
- `PushTo` takes `func(deviceID string) (Notification, bool)` (research item R2). `Push` wraps it. Each distinct notification is fitted once, and sealed for each device it is sent to. `Server.notificationFor` is the function: `mayPush` first, so a device that may not see pane names is sent nothing; then `approveSwitchOn`, which picks the approver wording. It does not use `mayApproveWithKey`: no key is proven at push time, and the sealed socket still holds every decision to the gate. A device whose grant is on but whose key later fails the gate is told "Allow or deny" and finds the card refused; T3 should show it why.
- The pane name and project in the title, and the names in the list of several panes, go through `capName`, for terminal panes too. After the security review `capName` is `safeLine(s, 40)`: format characters (bidi, zero-width) are dropped, a control character or a new line becomes a space, then the text is redacted, then cut to 40 runes with an ellipsis. It was `team.CleanLine(record.Redact(s), 40)`, which let bidi and zero-width characters through, dropped new lines so words ran together, and removed control characters after the redactor, so a secret with one in the middle was put back together. `capName` is also used by the relay-window scrub (`scrubPaneNames`, the needs list, tab titles, `relayteams`); those are names shown in a window, and the stricter cleaning only changes names that held such characters, and marks a cut name with an ellipsis. The tests that cover those callers pass unchanged.
- Residual, not fixable by cleaning: the name is the one the person or the agent chose, so an agent that names a pane (or a directory) "Deploy approved" or the name of another project can mislead on a lock screen. The wording around it is fixed and says nothing of the card, but the name is shown as it was given.
- The wording for a device with approvals on is checked again just before the push is sent (`recheckPush`, on the workspace goroutine). The card seq, the run token and `CanAllow` have to be the same as when the wait was noticed; if the card changed, is gone, or the check cannot run, every device is told "Answer on the desktop". The window that is left is the relay call itself.
- A tap opens `/d/HOST/PANE`, as for a terminal pane.

How it is kept from shipping before T3:

- `structuredPushLive` is a constant, `false`. The only other way to turn the push on is the unexported field `pushState.structuredOn`, which only tests set.
- `TestStructuredPushNeedsTheApprovalsSocket` fails if the constant is `true` and no non-test Go file in `internal/server` calls `Handle` or `HandleFunc` with the literal `"/ws/approvals"` (found by parsing the files, so a comment, a string or a test file does not count). T3 registers the route in `server.go` beside `/ws/artifacts` and flips the constant in the same change. `TestTheRouteCheckSeesARegisteredHandler` shows the check finds `/ws/artifacts` in the real tree, finds a registration in a temporary file, and does not take a comment, a string or a test file for one.
- The call has to name a handler `handleApprovals` (`s.handleApprovals` or `handleApprovals`), as `/ws/artifacts` has `handleArtifacts`, so a route registered with some other function does not satisfy it. A name proves nothing about what the handler does: reviewing T3 still has to check that it is the sealed one, and that the constant is flipped in the same change.

Deviations and choices:

- The audience for the approver wording is `approveSwitchOn`, as the brief said. The plan's section c (older text) said a structured wait goes only to devices with approvals on; decision 4 replaced that and this follows decision 4.
- A viewer with approvals on but without watch-panes is sent no push at all (`mayPush`). The plan does not say. Telling it would put a pane name on a device that is not shown pane names.
- A structured pane that is blocked is skipped, and a terminal pane that is blocked is still pushed: `TestABlockedStructuredPaneIsNotPushedButABlockedTerminalIs`.

## T3 as built

T3 (the sealed socket) was built on 28 Oct 2026 against origin/main 3e8a5d06. The code is `internal/server/approvals_socket.go` (socket, offers, limits, padding), with the gate in `approvals_gate.go` (T1) and the projection and lock in `approval_card.go` (T2). The audit log is `remote-approvals.log`, written by the same `auditLog` as the artifacts log. No API change: the sealed frames are not in `api/catalog.json` (1.27 was taken by T2; the next free minor would be 1.28 if a `sockets` section is added later). The phone app (T6) can be coded from this section.

### Connecting

`GET /ws/approvals` through the relay only. The relay sets `Flockdeck-Remote-Device`, `Flockdeck-Remote-Device-Name` and `Origin` as for `/ws/artifacts`; a request on the local listener is a 404, one without a device or an `Origin` is a 403. The phone runs the same end-to-end handshake as the terminal socket with its usual key (the first two frames, binary). A device that is refused is closed with a WebSocket close code and a one-word reason:

| Code | Reason | Meaning |
| --- | --- | --- |
| 4403 | `key` | no end-to-end key on one side |
| 4403 | `origin` | the request was for the desk-origin key; only the usual key may approve |
| 4403 | `disabled` | approval is off for this device, or the wording it agreed to is out of date. Also sent when the desk switches approval off while the socket is open |
| 4403 | `verify` | the desk has not marked this key verified. The key checked is the one the handshake was made with, not the roster's: a device that handshakes with another key than the one the desk verified gets this |
| 4403 | `grant` | approval was allowed for a different key than the one in use |
| 4403 | `refused` | three refusals in a row of the kind a correct client never causes, an unpadded request included (see Abuse and limits) |
| 4429 | `busy` | too many sockets, too many opens a minute, or too many frames turned away for rate |
| 1011 | `unavailable` | the host could not write its audit line |

After the handshake every frame, both ways, is one sealed frame as in `e2eConn`: a tag byte (1, text) then UTF-8 JSON, sealed with the session. All the JSON below is an object with an `op` field. Unknown fields are ignored on both sides, and unknown ops from the phone are ignored, so a newer phone can talk to an older host. The check that decides whether the device may approve (key, verified, approval on at the current wording, grant for this key) runs again before every frame the phone sends and before every frame the host sends on its own, from the preferences file. A device that fails it is closed with the reason above and is told nothing more.

### Padding

Every frame the host sends has a `"pad"` string field of `"0"` characters added so that its JSON plaintext (the bytes after the tag byte, after escaping) is a multiple of 1,024 bytes. The phone ignores the field.

Every request the phone sends must be padded the same way: its JSON plaintext must be a multiple of 1,024 bytes, at most 4,096, made by adding a `"pad"` string field of filler (the host ignores it). The host refuses a request of any other length with `{"op":"error","v":1,"code":"padding"}` and counts it as a refusal of the kind a correct client never causes. This is so that allow and deny, and the other requests, cannot be told apart by their length. The phone's code is, for a request object `r`: serialise it without `pad`, add `,"pad":"` + zeros + `"}` so that the total is the next multiple of 1,024 (the filler is `1024*ceil((len+9)/1024) - len - 9` characters, where 9 is the length of `,"pad":""`). The host's `padded()` in `approvals_socket.go` does exactly this.

Residual: padding hides length inside one bucket. A frame that grows past 1,024 bytes (several waiting panes, a long command) is seen by the relay as 2,048 and so on, so the relay can still tell roughly how many panes wait and whether a command is long. Timing also shows when a decision was made.

### Frames from the phone

```json
{"op":"hello"}
{"op":"ping"}
{"op":"approvalList"}
{"op":"approvalDecide","pane":"PANE","token":"7.0","cardSeq":7,"offer":"32 hex characters","decision":"allow"}
```

`decision` is `allow` or `deny`. Anything else, `allow_always` included, is refused. `pane`, `token`, `cardSeq` and `offer` are copied from an item of the last `approvals` frame. There is no `requestId`: the agent chose it, so it is not sent to the phone, and the host holds it against the offer.

The first request should be `hello` (to learn the limits; every request is padded, `hello` included) and then `approvalList`. The host sends nothing unprompted until `approvalList` has been sent. After it, the host sends a fresh `approvals` frame on its own whenever the waiting set changes (it looks every 500 ms), and after every decision. A phone that is open and idle should send `ping` at least every 2 minutes: a socket that sends nothing for 5 minutes is closed, and a new one has 1 minute to send its first frame.

### Frames from the host

```json
{"op":"hello","v":1,"desk":"DESK NAME","device":"DEVICE NAME","minDisplayMs":700,"maxItems":8,"maxRequest":4096,"padBucket":1024}
{"op":"pong","v":1}
{"op":"approvals","v":1,"items":[ITEM, ...]}
{"op":"approvalResult","v":1,"ok":true,"pane":"PANE","cardSeq":7,"decision":"allow"}
{"op":"approvalResult","v":1,"ok":false,"pane":"PANE","cardSeq":7,"error":"FIXED TEXT"}
{"op":"error","v":1,"code":"busy"}
```

`error` has `code` `busy` (frame rate) or `unavailable` (not a text frame, too long, or not JSON). `approvalResult.pane` and `cardSeq` are echoed from the request.

An item, for each structured pane that has a permission request pending, in tab order, at most 8:

```json
{
  "pane": "PANE", "label": "api", "project": "flockdeck",
  "token": "7.0", "cardSeq": 7, "offer": "32 hex characters",
  "answerable": true, "kind": "execute", "text": "go test ./pkg",
  "added": 0, "removed": 0, "canAllow": true, "why": "", "waitedMs": 1500
}
```

- `kind` is `execute`, `edit` or `other`. `text` is the command, or for an edit the path; it is empty for `other`. Draw it as text, never as markup. `added` and `removed` are the lines an edit adds and removes.
- `answerable` false means the phone has nothing to answer (a plan, a question, a card with no plain Reject): there is no `offer`, `canAllow` is false, and `why` is the fixed sentence to show ("Plan approval is on the desktop.", "Questions are answered on the desktop.", "This request is answered on the desktop.").
- `answerable` true with `canAllow` false means Deny only; `why` is the fixed sentence for the reason (the twelve of `approval_card.go`). `text` is then the redacted, clipped text.
- `canAllow` true means Allow once and Deny are both offered; `why` is absent.
- `label` is the pane's name (not its task), `project` the project name; both are redacted and capped as the desk's relay scrub does. Labels are not unique: two panes can have the same name, so the phone must show `project` next to `label` and tell cards apart by `pane`, never by label.
- `waitedMs` is how long this socket has known of the card, not how long the agent has waited.
- `offer` is made by the host for this card on this socket. A decision uses it once. When a card changes, ends, or its offer is used, the old offer is dead and a new `approvals` frame carries the new one.

The refusal texts of `approvalResult.error` are fixed, and none holds text from an agent:

| Text | When |
| --- | --- |
| `Too many answers too fast. Wait a moment and try again` | decision rate |
| `The answer needs a pane, a card number, an offer and allow or deny` | a field missing |
| `The answer must be allow or deny` | any other decision |
| `That request is not one this connection was shown` | an offer that is not 32 lowercase hex characters (counted as abuse) |
| `That answer is not for the request that was offered` | `pane`, `token` or `cardSeq` does not match the offer |
| `That request cannot be allowed from here` | allow for a card shown as Deny only |
| `That request was only just shown. Look at it, then answer` | less than 700 ms after the card was sent |
| `That request has already been answered or has ended` | the card ended or was answered by another before this arrived |
| `That pane has restarted since the request was shown` | the pane's runtime token changed |
| `That pane is not waiting for an answer` | no card is pending now |
| `That request is answered on the desktop` | the card is no longer one the phone may answer |
| `That request has no single option for that answer` | the card has no one `allow` or `reject` option |
| `The answer could not be recorded, so it was not given` | the audit line could not be written |
| `The answer could not be given` | the pane refused it for another reason |

### What the host does with a decision

1. Takes a decision token from the device's allowance (burst of 5, then 1 a second, shared by all its sockets).
2. Checks the fields are present and `decision` is `allow` or `deny`.
3. Looks the offer up among those made on this socket. One that is not 32 lowercase hex characters: refused, and counted as abuse. One of that shape that was used, ended, or never made on this socket (a second tap, a card the desk answered, or an offer kept from an earlier connection): the stale text, not counted. The last 256 are remembered, oldest forgotten first.
4. Uses the offer up, whatever happens next. A refused tap therefore gets a new offer in the next `approvals` frame, and the 700 ms starts again.
5. Checks `pane`, `token` and `cardSeq` against the offer, and that Allow was offered for the card.
6. Refuses it if 700 ms have not passed since the card was sent on this socket.
7. Reads the pane now (on the workspace goroutine), asks again whether the device may still approve (the desk's cut of the socket, then the gate with the handshake's key), runs `phoneAnswer` with the lock made when the card was shown (card number, the agent's request id, runtime token), checks the whole projection (kind, text, counts, canAllow, why) still reads as it did, and picks the option.
8. Writes the `decision` line to the log and flushes it to disk. If that fails, nothing is given.
9. Asks once more whether the device may still approve, immediately before the answer. A switch turned off, a kill switch, an unpair or a changed key while the line was being written stops it here. The reply is then `Approval from this device was switched off` and the socket is closed with the reason.
10. Gives `AnswerPermission` the lock's card number and request id with the option id. It takes the answer under the pane's own lock, so two answers (phone and desk, or two phones) cannot both win.
11. Replies `approvalResult`, then sends `approvals`. Every frame the host sends, these two included, is preceded by the same check; a device that fails it is sent nothing and is closed.

### Abuse and limits

Three refusals in a row of the kind a correct client never causes close the socket (4403 `refused`) and put a notice on the desk: an offer that is not of the shape of one, a missing field, a bad decision, a `pane`, `token` or `cardSeq` that does not match, an allow for a card not offered, a request that is not text or JSON, a request that is not padded. Only a successful answer resets the count; other valid requests (`hello`, `ping`, `approvalList`) do not, so the count cannot be kept down by interleaving them. Refusals that a correct client can cause do not count: a tap on an offer that was used, whose card ended, or that an earlier connection made (an impatient second tap, a card the desk answered, a phone that reconnected), a tap under 700 ms, rate, and the pane having restarted. The desk's buttons should be disabled after a tap until `approvalResult` or the next `approvals` arrives (T6). Frames over 10 at once then 2 a second get `error busy`, and more than 10 turned-away frames in a row close the socket with 4429. A device holds at most 2 sockets and may open 20 a minute. These numbers are starting values for the live check (decision 7).

### Audit log

`remote-approvals.log`, same format and rotation as the artifacts log (1 MiB, three files, owner-only). Events: `connect` (written when the first request has been opened, the proof that the device holds the key, not at the handshake), `decision` (written before the answer is given, and flushed to disk; the only line that is), `decision-refused` (with a `reason`; written at most once every 10 seconds for one device and reason, with `suppressed` counting the rest), and the desk's own `device-approve-on`, `device-approve-off` and `approvals-off`. `denied` lines (a socket or a device refused) are in a file of their own, `remote-approvals-denied.log`, so that refusals under forged device ids cannot rotate decisions out of the first. `shown` lines (a card sent to a socket) are in a third, `remote-approvals-shown.log`, because an agent can replace its card as fast as it is looked at. One is written only for a card the phone can answer, once for each card on a socket (a card offered again after a refused tap is not written again), and at most 20 a minute for a device and 60 for all devices (the count left out is in `suppressed`). The `decision` line carries the hash and length of what was shown, so a `shown` line left out loses no decision. A refusal that follows a `decision` line (`decision-refused`) is always written and is not held to the 10-second limit, and a socket the desk cut (kill switch, approval turned off, unpair, a changed key) is closed with no `denied` line and no notice of a refusal, since the desk's own change has its own. They are limited per device and reason and to 30 lines a minute across all devices (the count left out is in `suppressed`), and notices on the desk to 6 a minute. A `shown` line and a `decision` line carry `pane`, `name`, `seq`, `kind`, `len` (bytes of the text shown) and `hash` (its SHA-256 in hex), and a decision line `decision` (`allow_once` or `reject_once`). The text is not written. There is one `decision` line and no separate success line. The rule: a `decision` line means the host went on to give the answer. It was given unless a `decision-refused` line for the same card follows. That line is best effort: if the disk fails after the `decision` line, the refusal marker may be missing, and the desk is told that lines are being lost ("The phone approval log could not be written"). The `decision` line is the one thing that is never skipped: if it cannot be written and flushed, the answer is not given. The refusal reasons are `rate`, `shape`, `decision`, `offer`, `ended`, `mismatch`, `allow-not-offered`, `too-soon`, `stale`, `restarted`, `no-card`, `changed`, `audit`, `error` and, for a switch turned off mid-decision, the gate's word (`disabled`, `verify`, `grant`, `key`, `origin`). Details are in `.flockdeck/knowledge/data/approvals-audit-log.md`.

### Where the code disagreed with this page

1. The agent's `requestId` is not sent to the phone. It is agent text (an agent chooses it, up to the cleaning), and the plan says no agent text goes on a phone path. The offer already names the card; the host keeps the request id in the lock. The decision frame has `pane`, `token`, `cardSeq`, `offer` and `decision`.
2. `since` is `waitedMs`, and measures this socket's knowledge of the card. A time of day would depend on the phone's clock, and the host does not record when the card appeared.
3. The page said "three refusals in a row close the socket". Only refusals of the kind a correct client never causes count. A tap on a card the desk has just answered is a normal race and must not end a socket. A session that taps three stale cards in a row stays open (the rate limit still applies).
4. `hello`, `ping` and `pong` are added, and nothing is sent unprompted before the first `approvalList`.
5. Plans and questions are listed (`answerable: false`, a fixed `why`, no offer), so the phone can say "on the desktop". The plan said "for each waiting pane that this device may see".
6. The page said `approvalResult` has "ok or a fixed refusal text; the same texts as `permissionAnswer` where they exist". They are the texts of `phoneAnswer` (T2) and `permissionStale`; the rest are new and listed above.
7. New cards are noticed by looking every 500 ms, not by a subscription to the runtime. It keeps the socket to one goroutine for writes and means a card can reach the phone up to half a second late. The look no longer takes a snapshot of the pane: it asks `Runtime.PendingCard`, which is the bridge's pending card (one lock, one small copy), where `PendingPermission` used to build the whole `Snapshot` (every transcript entry, the queue and the shell list). With a socket open and the list asked for, the cost every 500 ms is one walk of the tabs on the workspace goroutine and one small copy for each pane that has a card. Not measured; reasoned from the code. Every goroutine of the socket ends with the handler (the context is cancelled on return, the connection closed, the ticker stopped).
8. A viewer with approvals on sees every waiting structured pane, as the page says. `watchPanes` is not consulted.
9. Only the usual key origin may approve. T1 ties the grant to it. A request that the relay marks as desk origin is refused with `origin`.
10. Verification is read with `DeviceVerifyState` for the usual key (as the turn-on does), not `DeviceVerified` for the desk key that `/ws/artifacts` checks. The key given to it and to `mayApproveWithKey` is the one the session was derived with (`e2e.Session.DeviceStaticKey`, added in this task), not one looked up in the roster before or after the handshake. The check before the handshake only asks whether approval is on for the device, so a device that is off costs no handshake.
11. The desk is not told when a device opens the socket (the artifacts socket tells it once a run). It is told of refusals and abuse closings, at a limited rate.

### Known limits

- The gate reads the preferences file through a cache of up to 5 seconds (T1). The record in memory must also say yes, and the sockets are cut at once when the desk switches a device off, so the cache only delays a file edit that narrows approval.
- A device that has approvals on is shown every pending card, not only the one a push named.
- `AnswerPermission` is called on the socket's goroutine, not the workspace goroutine, as it is atomic. The file-system check of an edit's path in `phoneAnswer` also runs there.
- The desk trace ("Allowed from PHONE") is T5. The desk's stderr line (`permissionLog`) now says "from a phone", with pane, card number and `allow_once` or `reject_once`.
- Not run: a real relay or a real phone. The relay's handling of the path is research item R1 and T7.
