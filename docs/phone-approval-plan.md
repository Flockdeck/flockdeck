# Phone approval plan

Written on 28 Oct 2026 against origin/main 0f97bdee (through #731). Design only; no code was changed or run for this page. The decision behind it is the user's: before structured panes become the default, a phone has to get a push when a structured pane waits for approval, and has to be able to allow or deny from the phone. This is item 1 of "What is still open" in the [parity audit](acp-parity-audit.md).

What I read: `internal/server/push.go`, `relayscrub.go`, `viewerrole.go`, `permission_answer.go`, `structured.go`, `structuredrewind.go`, `artifacts_socket.go`, `artifacts_consent.go`, `artifacts_audit.go`, `internal/e2e/e2e.go`, `internal/remote/webpush.go`, `internal/store/deviceaccess.go`, `docs/structured-pane.md`, `ui/help/pages/remote.md`, `api/README.md`, and the card code in `internal/acp/bridge` and `claudeacp/perm.go`. Not available here: the relay (`flockdeck-relay`) and the phone app (`flockdeck-remote`). Everything this page says about them comes from comments and docs in this repo, and is marked where it matters.

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

- The pane label is `p.Name`, the one terminal pushes already use. `LabelAuto` labels are made from the task and must not be used; `scrubPaneNames` already treats them that way. Unsure whether `p.Name` can ever be task-derived; T4 checks.
- The wait is pushed on the existing timer and rules: the delay (30 s default), one push a minute, the desk-in-use check, `phoneLook`, per-pane mute. Nothing new there.
- Structured waits are pushed only to devices with approvals on for them. Terminal waits go to all devices as now. `PushTo` already takes a per-device filter (`mayPush`); this needs a second filter keyed on the kind of wait, so one push call may need splitting by audience. Unsure how `PushTo` batches; T4 reads it before sizing.
- No Approve and Deny buttons on the notification. A lock-screen tap would approve something the person has not seen. A tap opens the approvals page, `/d/HOST/PANE?approve`.

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
- `token`. The pane runtime's identity, like `structuredRewind`'s `rewindToken` (`seq.epoch`). The card `seq` is per bridge, so after a pane restart a stale answer could carry a `seq` and `requestId` that match a different new card. The token changes on restart and on rewind, so it cannot. Whether `RewindToken` itself is enough, or the token should also cover a restart that keeps the runtime, needs a look at `internal/structured/rewind.go`; T2 settles it.
- `cardSeq` and `requestId`, checked inside the existing atomic `AnswerPermission`, so two answers (phone and desk, or two phones) cannot both win.
- A decision arriving less than 700 ms after the card was sent to that socket is refused. This stops a stray tap on a card that appeared under the thumb. Unsure it is worth a rule; it is cheap.
- Rate: per device, a burst of 5 decisions and then 1 a second; 3 refusals in a row close the socket and put a notice on the desk. The numbers are a guess and want the live check.
- No queueing. If the socket is down when the person taps, the phone says so and the tap does nothing.

Roles. Approval is a separate switch per device and is off for everyone by default, including existing full devices: the "no record means full" compatibility rule is not extended to it. It works for either role. A viewer with the switch on can see the cards sent to it and decide them, and still cannot type, resize or open panes. A full device is not allowed to approve just for being full. `mayPush` stays as it is for terminal waits; a structured wait goes to a device only if it has approvals on.

The desk's part: turning a device on shows fixed text (a wording version, as `artifactAckVersion`): "This device will be shown the commands and file paths your agents ask to run or change, and can allow or deny them. The relay cannot read them." It requires the verified-key mark. It is checked again on every frame from the preferences file, so a switch turned off takes effect on the next frame.

### d. What the relay carries and learns

Carries: one more WebSocket path per device, like `/ws/artifacts`. Whether the relay routes by an allowlist of paths is something I could not check from here. If it does, the relay needs the new path added; if it proxies any `/ws/*`, it needs nothing. Pushes need no relay change: the sealed per-device messages are already opaque to it.

Learns, beyond today: that a device used the approvals socket, and when; the size of each sealed frame; and the timing of frames, which shows roughly when a decision was made. It does not learn which pane, what was asked, or what was answered. Frame size would give away roughly how long a command is. Padding frames to a fixed bucket (the push path pads to 1,024 bytes, `pushRecord`) would close that; I do not know whether the `e2e` frame format has room for it, and T3 should check.

The control-socket alternative would have the relay learn the pane, the card, the tool and the decision, and let it forge one. That is the difference that matters.

Does it learn less than for a terminal pane today? About the same. A terminal prompt answered on the phone is also sealed, and the relay sees timing and size.

### e. Audit log

A new file, `remote-approvals.log`, in the state folder, using the artifacts audit machinery (`auditLog`, 1 MiB, three files, owner-only). One JSON line for: socket opened, socket refused (with reason), card shown, decision made, decision refused (with reason), and each desk change (approvals on or off for a device, global switch). Fields: time, event, device id, device name, pane id, pane name (cleaned), card seq, decision (`allow_once` or `reject_once`), tool kind, the length of the text, and a SHA-256 of the text that was shown.

It does not store the command or path. Redaction is best-effort, and a log that keeps commands keeps whatever it missed. The hash lets someone with the conversation record check that the card they see is the one that was approved. Unsure whether the conversation record holds the card text and the outcome; if it does not, the hash proves less than I would like, and the log may need the redacted text after all (decision 6).

The desk side also gets a visible trace: the card on the desk, if it is open, changes to "Allowed from PHONE NAME at TIME" and a notice appears. Unsure how much of this the bridge's conversation entries support; T6.

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

The full interface reached through the relay (the desk-origin UI in `ui/`) needs a small change as well: today its cards say "details on the desktop" for a relay window. Whether the desk-origin UI also gets the approvals page, or only the phone app does, is decision 7.

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

## Staged tasks

Sizes: S under half a day, M about a day, L two or more days.

| # | Task | Size | Depends on |
| --- | --- | --- | --- |
| T1 | Prefs and desk: per-device `approve` flag, global off, acknowledgement version, `setDeviceApprove`, `setApprovalsOff`, `flockdeck remote approvals ...`, fields in `remoteDevices` and `hello`, catalog and access table. API 1.26. | M | none |
| T2 | Card projection and decision mapping: pure functions with the table above, adapter identity on `StructuredPane`, the token, bidi stripping, the redaction rule. Heavy table and fuzz tests. | M | none |
| T3 | `/ws/approvals`: handshake reuse, gate, offers, `approvalList`, `approvals`, `approvalDecide`, `approvalResult`, limits, abuse closing, padding if possible. | L | T1, T2 |
| T4 | Push for structured waits: lift the `p.Sess == nil` skip, per-device filtering, the four wordings, plan and question variants, tests. | M | T1 (answerability from T2) |
| T5 | Audit log, desk notice, "Allowed from PHONE" on the card. | S | T3 |
| T6 | Phone UI in `flockdeck-remote`: approvals page, card, offline and stale states, tap from push. Different repo; size is a guess. | M | T3 |
| T7 | Relay: carry the new path if it allowlists paths. Different repo; may be zero. | S | T3 |
| T8 | Docs: `structured-pane.md` (the relay lines that stop being true), `remote.md` (what the relay can see, the new switch), parity audit and matrix row 8, help page. | S | T3, T4 |
| T9 | Live check, steps 1 to 11, written up. | S | T3 to T7 |

T4 should not ship before T3. A push that sends the user to a page where they cannot answer is the reason #378 skipped structured panes.

If the schedule needs a smaller first cut: T1, T2, T3 and T4 with Deny-and-Allow for `execute` cards only. Edits can follow.

## Decisions for the user

1. Transport. Recommended: the sealed `/ws/approvals` socket, which costs an L task and needs the key to be verified before a device may approve. The alternative is new commands on the control socket, which is a few days faster but means the relay reads the command and could forge an approval, and I do not recommend it. Is requiring a verified key right, given that most phones will not have been verified?
2. Plans. Recommended: desk only in the first version. Or: show the plan in full on the phone, which is a bigger UI task and needs markdown handling that cannot load images or links.
3. Edits. Recommended: allow with the path and line counts, no diff. Alternative: commands only in v1, edits deny-only. Commands only is safer and smaller.
4. Agents. Recommended: allow only for Claude and Codex, because Flockdeck wrote their option ids. Is deny-only for opencode and Gemini acceptable while they are not the default?
5. Defaults. Recommended: off for every device, including existing full devices, switched on per device at the desk. Alternative: on by default for full devices, which matches how roles work today but gives every already-paired phone a new power without a prompt.
6. Audit. The log keeps a hash and the length, not the text. Is that enough, or should it keep the redacted text for the period of the log's rotation? I could not tell whether the conversation record holds the card.
7. Where the UI goes. Phone app only, or the desk-origin interface on a phone as well? The second is more work and doubles what has to be reviewed.
8. Push wording for a wait the phone cannot answer. Recommended: still push, saying "Answer on the desktop". Or no push, as today. A user who is away may prefer to know.
9. Minimum display time (700 ms) and the rate limits. I put numbers in, none are measured.

## What I am not sure of

- Whether the relay allowlists paths, and whether it logs frame sizes or timing.
- Whether `PushTo` can send different messages to different devices in one call, or the push must be split.
- Whether the e2e frame format can be padded.
- Whether `RewindToken` changes whenever a pane's bridge restarts and card seq resets. If it does not, a second token is needed.
- Whether `p.Name` can be derived from the task.
- Whether the conversation record keeps a permission card and its outcome.
- How the phone app is structured, and whether the desk-origin UI shares code with it. I did not see `flockdeck-remote`.
- That a real Claude card ever has more than one `allow` option, or an `allow` that is not once. The adapters in this repo use `allow` and `reject` as constants; no real claude was run for this page.
- The rate and timing numbers.
