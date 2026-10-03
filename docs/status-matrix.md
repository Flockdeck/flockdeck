# Pane status matrix

This document lists every situation a pane can be in and the status it should show for each one. It also records how that status is signalled today and whether the current code gets it right.

The tests named in the last column walk this table row by row. Each test case carries its row ID, so a failure points at the row that no longer holds.

A row marked **✗** is a confirmed bug. Its test asserts the *correct* behaviour and is skipped through a `knownBug` helper, so CI stays green. To run the known bugs and watch them fail:

```
FLOCKDECK_STATUS_BUGS=1 go test ./internal/session/ ./internal/workspace/ ./internal/server/ -run StatusMatrix
```

When one of these bugs is fixed, delete the `bug:` field or the `knownBug` call from its case, and change ✗ to ✓ here.

No row is marked ✗ today. The 15 cases that were (rows B7, B12, B21, C1, C2, C4, C14, C18 and F4) are fixed, mostly by the turn tracking described at the end of section C, and their tests run in normal CI.

Legend:

- ✓: the current code satisfies the row.
- ✗: confirmed bug; the test is skipped.
- ~: accepted limitation. The test pins today's behaviour, and the Notes column explains why it is accepted.

Test file abbreviations:

- **S**: `internal/session/statusmatrix_test.go`
- **W**: `internal/workspace/statusmatrix_test.go`
- **H**: `internal/hooks/statusmatrix_test.go`
- **V**: `internal/server/statusmatrix_test.go`
- **G**: `internal/chat/statusmatrix_test.go`

## The status model

`session.Status` has six values:

| Status | Meaning | NeedsAttention |
|---|---|---|
| `starting` | The process has been spawned and nothing has been learned yet. | no |
| `working` | The agent is busy and does not need the user. | no |
| `waiting` | The agent is blocked on the user: a permission prompt, a question, or an elicitation. | **yes** |
| `blocked` | The turn ended right after a tool call was refused outright, and nothing since has recovered it. | **yes** |
| `idle` | The turn is over and the agent is at its prompt. | no |
| `exited` | The process is gone. A pane that failed to start (`Pane.Sess == nil`) also reads as exited. When it exited with an error or was killed, the UI draws it as failed (see E2). | no |

There is no `failed` status. The web UI derives "failed" itself, from `blocked` or from a start error (`Pane.Err`); see section F.

A status is decided in one of two ways:

1. **Lifecycle events (authoritative).** Claude Code runs `flockdeck hook --event X` for each subscribed event. `hooks.Emit` posts the event to the loopback server. `workspace.handleHook` then applies it in this order:
   1. Launch and session filter.
   2. `NoteBackground`, and `SetBackground` for a Stop that lists what is still in flight (see section G).
   3. `session.StatusForEvent`.
   4. `Session.ResolveBlocked`.
   5. `Session.SetStatusFull`.

   Steps 3 to 5 are one call, `Session.ApplyEvent`, which also tracks the turn (see the end of section C). Every event reaches it, including those `StatusForEvent` maps to no status.

   The first event that changes status sets `hooksSeen`, and so does a `SessionStart` (any source but `compact`), which also settles a pane the hooks have not yet spoken for at idle (A16, D12). From then on the output-based guesses below are switched off for that pane. The chat client (`internal/chat/lifecycle.go`) posts the same event names through the same `Emit` function.
2. **Output inference (fallback).** This applies to shells, to agents whose Spec reports no lifecycle, and to a hooked agent before its first `SessionStart` or status-changing event (for good, if its hooks never arrive). It reads the pane's output in `Session.publish`:
   - Bytes arriving mean `working`, except output that is only the program drawing itself (`Session.ownRedraw`): its first screen, within `startupGrace` (5 s) of starting with nothing typed, leaves it `starting`; a redraw within `resizeEcho` (1 s) of Flockdeck resizing it, with nothing typed since, leaves it as it was (D14–D17). Output that goes on past either window is `working`, and a pane already working stays so.
   - `quietBeforeIdle` (3 s) of silence means `idle`.
   - An agent's bell after startup means `waiting`.
   - The Spec's patterns can mean `waiting` or `idle`.
   - Typing into the pane answers an inferred wait.

   Separately, `Session.Write` and `settleAnswered` let Enter answer a *hook-reported* permission prompt.

## A. What each lifecycle event means on its own

| Row | Event (payload) | Expected | Today | ✓? | Test |
|---|---|---|---|---|---|
| A1 | `UserPromptSubmit` | working | `StatusForEvent` | ✓ | S `TestStatusMatrixEventMapping` |
| A2 | `PreToolUse` (any tool) | working, detail = tool | 〃 | ✓ | S |
| A3 | `PreToolUse` `AskUserQuestion` | waiting, detail = AskUserQuestion | 〃 | ✓ | S, H |
| A4 | `PostToolUse` | working, detail cleared | 〃 | ✓ | S |
| A5 | `PostToolUseFailure` (not an interrupt) | working: the turn goes on | 〃 | ✓ | S, H |
| A6 | `PostToolUseFailure` with `is_interrupt` | idle: Claude Code returns to its prompt with no Stop | `Emit` rewrites it to `Interrupted` | ✓ | S, H |
| A7 | `PermissionRequest` | waiting; keeps the tool from the preceding PreToolUse | `StatusForEvent` + `SetStatusFull` carry-forward | ✓ | S |
| A8 | `PermissionDenied` | working (the turn goes on); marks the turn as denied | `StatusForEvent` + `ResolveBlocked` | ✓ | S |
| A9 | `Notification` `permission_prompt` | waiting, keeping the tool | 〃 | ✓ | S, H |
| A10 | `Notification` `idle_prompt` | no change when idle, waiting or blocked; see C14 for a pane still showing working | `StatusForEvent` gives `ok=false`; `ApplyEvent` turns a *working* pane idle (C14) | ✓ | S, H |
| A11 | `Notification` `elicitation_dialog`, `agent_needs_input`, unknown types, or the chat client naming a tool | waiting | `StatusForEvent` | ✓ | S, H |
| A12 | `Notification` with no type (Claude Code older than 2.1.269) | waiting | 〃 | ✓ on an idle pane (see B17) | S, H |
| A13 | `Notification` `auth_success`, `elicitation_complete`, `elicitation_response`, `agent_completed`, `computer_use_exit` | not reported at all | dropped in `Emit` (`finishedNotifications`) | ✓ | H |
| A14 | `Stop` | idle, or blocked (see B9) | `StatusForEvent` + `ResolveBlocked` | ✓ | S, H |
| A15 | `StopFailure` (the turn ended on an API error) | idle, or blocked after a denial | 〃 | ✓ | S, H |
| A16 | `SessionStart` | `compact`: no status change, it fires mid-turn (B13). Any other source (`startup`, `resume`, `clear`): no change once the hooks have reported; on a pane they have not, idle, and output inference is off from then on (D12) | `StatusForEvent` gives no status; `ApplyEvent` settles a pane with `hooksSeen` unset at idle. The source drives background bookkeeping, and any source but `compact` clears a denial mark (C18) | ✓ | S, S `D12`, `D14` |
| A17 | `SessionEnd` | no status change: `/clear` fires it and carries on, and a real exit is seen by the PTY reader | ignored | ✓ | S, and existing `TestNoLifecycleEventCanMarkALivePaneExited` |
| A18 | `SubagentStart` / `SubagentStop` | no status change of their own; they count background work | `NoteBackground`; a SubagentStop also ends what a background subagent from an older Claude Code left showing after the turn (B21) | ✓ | S |
| A19 | Any other event, or an event not subscribed to (`PreCompact`, …) | no change | default case | ✓ | S |

The events in A5–A8, A15 and A18 are only subscribed to on Claude Code 2.1.269 or later (`laterHookEvents`). On an older Claude Code they never arrive: a refused or failed turn stays `working` until the next prompt, and background subagents are not counted.

## B. Turns: sequences of events

| Row | Situation | Expected | How it is signalled today | ✓? | Test |
|---|---|---|---|---|---|
| B1 | A prompt and a turn with no tools | working, then idle | UserPromptSubmit, then Stop | ✓ | S `TestStatusMatrixTurns`, G G1 |
| B2 | A turn with tools | working (detail = the running tool, cleared between tools), then idle | Pre/PostToolUse, then Stop | ✓ | S, W, G G2 |
| B3 | Parallel tool calls with interleaved Pre and Post events | working throughout, then idle | 〃 | ✓ | S |
| B4 | Permission prompt answered **Yes** with Enter | waiting(tool), then working(tool) the moment Enter is pressed, then PostToolUse, then idle at Stop | `Write` treats Enter as the answer to a `toolQuestion` | ✓ | S `TestStatusMatrixKeyboard`, existing `TestEnterAnswersAPermissionPrompt` |
| B5 | Permission prompt answered **No** (arrow keys, then Enter) | idle: Claude Code returns to its prompt with no Stop | Enter gives working; `settleAnswered` gives idle after 10 s with no hook and no output | ✓ | S, existing `TestARefusedPermissionPromptGoesBackToIdle` |
| B6 | A permission dialog: PermissionRequest, then about 6 s later a `permission_prompt` Notification | waiting, keeping the tool name and its input (for the phone card) | `SetStatusFull` carry-forward | ✓ | S, W |
| B7 | Permission prompt refused with **Esc**, which is Claude Code's own "No, and tell Claude what to do differently (esc)" | working, then idle once quiet (same as B5) | `Write` treats a lone `\x1b` like Enter for a `toolQuestion`, and `settleAnswered` then gives idle. Before this, only `\r` counted and the pane stayed amber at an empty prompt. | ✓ | S `B7` |
| B8 | AskUserQuestion | waiting(AskUserQuestion). Its nudge keeps it waiting. Enter does not clear it, because Enter moves between questions. Its PostToolUse means working, and Stop means idle. | PreToolUse special case; `toolQuestion` stays false | ✓ | S |
| B9 | Denial: PermissionDenied, then the turn ends | **blocked**, naming the tool (reported once) | `ResolveBlocked`. It is cleared by a successful PostToolUse or a new UserPromptSubmit, and an ordinary PostToolUseFailure does not set it. | ✓ | S, W, existing `blocked_test.go` |
| B10 | The turn ends on an API error (StopFailure) | idle, or blocked if a denial is outstanding | 〃 | ✓ | S |
| B11 | The user stops a running tool (Esc or Ctrl+C) | idle. It is not blocked even after a denial, because the user is already at the pane. | `is_interrupt` gives `Interrupted` | ✓ | S, W |
| B12 | The user presses **Esc while the model is streaming text** (no tool running) | idle | Claude Code fires **no Stop** ("Stop does not run on a user interrupt") and no PostToolUseFailure. The `idle_prompt` Notification about 60 s later is the only signal, and `ApplyEvent` turns the working pane idle on it (C14). Before this, it was ignored and the pane stayed working forever. | ✓ | S `B12` |
| B13 | Auto-compaction mid-turn (`SessionStart` source `compact`) | stays working; background work is kept | SessionStart is ignored for status | ✓ | S, W |
| B14 | `/clear` at the prompt (SessionEnd, then SessionStart `clear`) | stays idle; background work is forgotten; the conversation id follows | `handleHook` | ✓ | S, W, existing `clear_test.go` |
| B15 | Resume (`SessionStart` `resume`) | no change on a pane the hooks have reported for; a freshly restarted one (hooks not yet seen) is idle (D12, D14); background work is kept | 〃 | ✓ | W, S `D14` |
| B16 | The `idle_prompt` nudge arrives after a turn ended | stays idle: never amber, never pushed | ignored | ✓ | S, W, existing `TestIdleReminderIsNeverPushed` |
| B17 | Claude Code older than 2.1.269 sends an **untyped** idle nudge after a turn ended | idle | an untyped Notification with no tool named, landing on a pane the hooks have left idle with no background work counted, is ignored (`Session.IsStaleNudge`). A real ask comes mid-turn, when the pane is already working or waiting, so it is not affected. `PermissionRequest` cannot be the tell: an older Claude Code does not send it. | ✓ | S, W |
| B18 | A repeated nudge for the same wait | no change report, and the wait clock is not restarted | `SetStatusFull` dedupe | ✓ | S `TestStatusMatrixWaitClock` |
| B19 | Manual `/compact` typed at an idle prompt | working while it compacts, idle when done | `PreCompact` (trigger `manual`) shows working; `PostCompact`, or the `SessionStart` `compact` that follows it on a Claude Code without that event, ends it (`Session.CompactionStatus`). An automatic compaction says nothing, so B13 is untouched. A quiet fallback (`SettleWhenQuiet`) covers a compaction that never reports its end. Both events are subscribed only from 2.1.269 (`laterHookEvents`); an older one still shows idle. | ✓ | S, W |
| B20 | Foreground subagent (Task): its tool calls fire hooks under the pane's session | working until the parent turn's Stop, then idle, with nothing left counted | Pre/Post events plus SubagentStart/Stop bookkeeping | ✓ | S, W |
| B21 | **Background subagent** that is still working after the parent turn's Stop | **steadily** idle (or blocked, if the turn ended on a denial), with BackgroundTasks > 0, through every one of its tool calls; waiting while it asks the user something, and back to idle once that same subagent's call goes on; BackgroundTasks back to 0 once it ends | A subagent's tool events carry `agent_id` (Claude Code 2.1.283). Once the turn is closed, `ApplyEvent` shows none of them: the pane stays on what the Stop left and reports no change. A subagent's question or permission prompt is still shown, naming the tool it kept back. Only the asking subagent's next Post event ends the wait. The subagent is counted from SubagentStart until SubagentStop, its `<task-notification>` or a Stop that no longer lists it (section G). A Claude Code that sends no `agent_id` falls back to the stray-PreToolUse counting below: working while each call runs, then back to the Stop's status. | ✓ | S `B21`, `TestStatusMatrixBackgroundSubagentIsSteady`, W `B21` |
| B22 | Background shell (`run_in_background` Bash) | idle once the turn ends, with BackgroundTasks = 1 **until the command ends**, however it ends | Counted from its PostToolUse (`backgroundTaskId`). Ended by KillShell/TaskStop, by the `<task-notification>` UserPromptSubmit Claude Code sends when it ends, or by a Stop whose `background_tasks` no longer lists it. `/clear` forgets it. See section G. | ✓ | H `TestEmitReportsBackgroundWork`, `TestEmitReportsWhatAStopSaysIsStillRunning`, W `B22`, `closefinished_test.go` |
| B23 | Flockdeck chat client turns | same as B1, B2, B4 and B5: working, waiting(tool) at a question, working again whether allowed or refused, idle at the end (also after an interrupt, and after an API key is found) | `reporter` sends events synchronously, in order, from one goroutine | ✓ | G `TestStatusMatrixChatTurns`, existing `declinedstatus_test.go` |

## C. Ordering, races and lost events

The hypothesis was: "a PostToolUse or PreToolUse for the turn's last tool call can arrive after the turn's Stop and flip the pane to working with nothing to flip it back."

**Verdict: confirmed, with a narrower trigger than suspected.**

- **What happened before the fix.** Nothing in `handleHook` has any notion of a turn or of event order. A late event is applied like any other. Once `hooksSeen` is set, no timer or output rule can return a `working` pane to `idle`. So *if* a tool event lands after its Stop, the pane stays working until the next prompt (C1, C2).
- **When the inversion can happen.** Claude Code waits for each command hook to exit before it goes on, and `Server.handle` applies an event before it replies. When every hook finishes in time, events are therefore applied in the order they fired (C0).
- **The trigger.** Inversion needs a hook that **gives up**: `Emit`'s 3 s HTTP deadline, or Claude Code's 5 s hook timeout (`hookSpec.Timeout`). The server does not know the hook gave up, so it still applies that event later, *after* the ones that followed it (C13, proven in H). Hooks slow to start is exactly the Windows situation `latehook_test.go` already describes: antivirus scanning a freshly started `flockdeck.exe`, a loaded machine, or a hook outliving its process.
- **More frequent causes of the same symptom:**
  - **B12**: Esc while the model streams. There is no Stop at all.
  - **B21**: a background subagent's tool calls after the Stop.
  - **C14**: any lost Stop.

  All three end in "working at an empty prompt" for the same underlying reason: a hook-reported `working` never decays, and the one signal Claude Code *does* send once it is sitting at its prompt, `idle_prompt`, is thrown away.

| Row | Situation | Correct behaviour | Today | ✓? | Test |
|---|---|---|---|---|---|
| C0 | Hooks that each finish in time | applied in firing order | Claude Code awaits each hook, and the server applies before it replies | ✓ | H `TestStatusMatrixAwaitedHooksArriveInOrder` |
| C1 | **Late PostToolUse** (or PostToolUseFailure) for the last tool, delivered after the turn's Stop | idle. A Post event with no PreToolUse since the last Stop belongs to the finished turn and must be ignored. (A background subagent's Post always follows its own post-Stop Pre, so this rule does not misfire on B21.) | `ApplyEvent` drops it: the turn is closed and no PreToolUse has been counted since | ✓ | S `C1` ×2, W `C1` |
| C2 | **Late PreToolUse** delivered after the Stop ("Stop before PreToolUse") | Cannot be told from a background subagent's PreToolUse at arrival. It must still **recover**: the `idle_prompt` that follows means idle (see C14). | working; its own late PostToolUse, or the `idle_prompt`, returns it to idle | ✓ | S `C2` |
| C3 | Duplicate Stop | idle, no change report | dedupe | ✓ | S |
| C4 | Duplicate Stop after a denial | stays **blocked** until something new happens (a prompt or a successful tool) | `ResolveBlocked` keeps the mark past the Stop that reports it, until a prompt, a successful PostToolUse, a SessionStart or an interrupt clears it | ✓ | S `C4` |
| C5 | A `permission_prompt` Notification arriving after the Stop | waiting. Accepted, because a background subagent can legitimately ask for permission after the parent's Stop. | waiting | ~ | S |
| C6 | An event carrying a stale `Launch` (from the process before a restart) | dropped | `handleHook` launch check | ✓ | W `TestStatusMatrixHookRouting`, existing `latehook_test.go` |
| C7 | An event with no `Launch` (a hook older than the launch check) | applied to the pane | 〃 | ✓ | W |
| C8 | An event for an unknown session id, or for one pane among several | dropped, or applied to that pane only | `w.panes` lookup | ✓ | W |
| C9 | An event for a pane with no session (start failed, or mid-restart) | dropped | `sess == nil` check | ✓ | W |
| C10 | Any event after the process exited | stays exited | `SetStatusFull` refuses to leave `exited` | ✓ | S `TestStatusMatrixExited`, W `TestStatusMatrixAfterExit` |
| C11 | SessionEnd while the process is still alive | no change; only the PTY reader's exit gives `exited` | A17 | ✓ | S |
| C12 | Hook cannot reach Flockdeck (not running, or connection refused) | the hook fails fast and silently (stderr only) and never blocks the agent; the event is lost | `Emit` returns an error; `hook` prints it to stderr | ✓ | existing `TestEmitToDeadServerIsNotFatal` |
| C13 | Hook times out (Flockdeck slow; hook slow to start) | the agent is not held up; *ideally* the late event is not applied out of order | `Emit` gives up after 3 s, but the server still applies the event later, after newer ones. The transport is unchanged; `ApplyEvent`'s turn tracking keeps such a late event from undoing the turn's end (C1, C2). | mechanism for C1, C2 and C5 | H `TestStatusMatrixAHookThatGaveUpLandsAfterTheNextOne` |
| C14 | **Missed Stop** (C12, C13, B12) | idle once Claude Code's `idle_prompt` arrives (about 60 s after it returns to its prompt). The nudge must turn a *working* pane idle, while leaving waiting and blocked panes alone. | On `idle_prompt`, `ApplyEvent` closes the turn and turns a working pane idle. A denial in a turn that ended this way is not reported as blocked: nothing says whether the Stop was lost or the user pressed Esc. | ✓ | S `C14`, W `C14` |
| C15 | Missed UserPromptSubmit | working from the first evidence of the turn: a tool event at once; for a turn with no tools, Enter at the idle prompt followed by output that keeps drawing (`Session.guessTurn`). The Stop ends it, and silence does if no hook ever comes. | Enter that draws once and stops (a slash command, an empty line) is not taken for a turn. | ✓ | S |
| C16 | Hook payload unreadable (bad JSON on stdin) | the event is still reported, without tool detail | `Emit` ignores decode errors | ✓ | H |
| C17 | PermissionRequest answered before the 6 s Notification, which then arrives anyway | working. A stale nudge turns the pane amber again until its PostToolUse. | amber until PostToolUse | ~ self-heals | none (same class as C13) |
| C18 | A denial, then a new conversation (`SessionStart` from `/clear`), then a Stop with no prompt in between | idle: `ResolveBlocked` documents that SessionStart clears the mark | `handleHook` hands every event to `ApplyEvent`, which clears the mark on a SessionStart of any source except `compact` | ✓ | S `C18`, W `C18` |

**The fix** is in `internal/session/turn.go` (`Session.ApplyEvent`) and follows the direction below. `handleHook` hands every event to `ApplyEvent`, which keeps a per-session turn state. The state is unknown until the first prompt or turn end. It is open from UserPromptSubmit, and closed from Stop, StopFailure, an interrupt, `idle_prompt` or a `/clear`. While the turn is closed:

- A Post event (PostToolUse, PostToolUseFailure, PermissionDenied or Interrupted) is dropped if no PreToolUse has been counted since (C1).
- A subagent's tool event (one carrying `agent_id`) changes nothing, except that a question or permission prompt is shown and that subagent's next Post event ends it (B21).
- A PreToolUse naming no subagent is applied as before, because it may be a background subagent's from a Claude Code that does not name it, and it is counted. Each Post event that follows uncounts one. At zero, the pane returns to what the turn's end left it showing: blocked if a denial is still marked, otherwise idle (B21, and C2 when the late Pre's own Post also arrives).
- A SubagentStop returns a working pane with anything still counted to the same place (B21).
- A real ask (a permission prompt, a question or an elicitation) is still applied, because a background subagent can ask (C5).

In any turn, `idle_prompt` turns a *working* pane idle and closes the turn (B12, C2, C14). A waiting or blocked pane is left alone. Output inference, compaction (a `compact` SessionStart touches no turn state) and background bookkeeping (#75) are unchanged. An event that was mapped to a status but dropped as stale still counts as the agent reporting, for `settleAnswered`.

A pane whose turn state is unknown (a fresh start before its first prompt, or a restart) applies events exactly as before.

The original recommendation, for reference:

1. Treat `idle_prompt` as idle when the pane shows `working`. This fixes B12, C2 and C14, and B21's tail, with one line in `StatusForEvent` or `handleHook` and no new timing.
2. Keep a per-session "turn open" flag: set it on UserPromptSubmit or on PreToolUse after a Stop, and clear it on Stop, StopFailure or Interrupted. Drop a Post event while the turn is closed and no PreToolUse has opened it again. This fixes C1.
3. Let SubagentStop return a closed-turn pane to idle. This fixes B21.
4. Treat a lone `\x1b` like `\r` for a hook-reported `toolQuestion`. This fixes B7.
5. Leave `blockedTool` set until something new clears it, and run `ResolveBlocked` for SessionStart. This fixes C4 and C18.

## D. Output inference (no lifecycle hooks)

| Row | Situation | Expected | ✓? | Test |
|---|---|---|---|---|
| D1 | A pane starts | starting (see D16 for what its first screen does) | ✓ | S `TestStatusMatrixOutputFallback` |
| D2 | It prints output, then goes quiet for 3 s (a shell running a build; a no-hook agent streaming an answer) | working, then idle | ✓ | S, S `TestStatusMatrixOwnRedraw`, existing `TestOutputMarksAPaneWorkingUntilItGoesQuiet` |
| D3 | It prints steadily | working; the busy clock is not restarted | ✓ | existing (same test) |
| D4 | An agent rings the bell after startup (5 s, or once typed into) | waiting; a bell during startup is ignored | ✓ | S, existing `TestBellWhileStartingUpIsNotAttention`, `TestBellReachesAPaneNobodyHasTypedInto` |
| D5 | A shell rings the bell | never waiting | ✓ | S |
| D6 | Typing answers an inferred wait | working, then idle once quiet | ✓ | existing `TestAnsweringAPaneClearsAnInferredWait` |
| D7 | Focus, mouse and terminal-reply sequences | not an answer | ✓ | existing `TestATerminalsRepliesAreNotAnAnswer`, `TestScrollingIsNotAnswering` |
| D8 | Output while waiting | stays waiting | ✓ | S, existing `TestOutputDoesNotOverruleTheBell` |
| D9 | Once a status-changing hook has arrived | output and bells are ignored, and a hooked `working` never settles on quiet (this is the flip side of C14) | ✓ | S, existing `TestOutputDoesNotOverruleALifecycleHook`, `TestBellIsIgnoredOnceHooksReport` |
| D10 | Spec patterns (for agents without hooks) | the patterns decide waiting or idle; an answered question is not re-read | ✓ | existing `TestPatternsSharpenTheFallback`, `TestAnAnsweredQuestionIsNotAskedAgain` |
| D11 | Exited | stays exited whatever else is printed | ✓ | S |
| D12 | A Claude pane that has only sent SessionStart (no prompt yet) | **idle**, and no longer read from output: SessionStart is proof its hooks report. (This row used to pin "still read from output: working while it draws, idle once quiet", which is what made every restored agent, and every agent in a project switched to, show working: its resume redraw, the redraw for each window fitted to it and its own status-line refreshes all read as work until somebody typed.) A hooked agent whose hooks never arrive at all is still read from its output. | ✓ | S `D12` |
| D13 | Jev assist for an agent without hooks that went quiet | may re-classify an idle guess as waiting | ✓ | existing `assist_test.go` |
| D14 | A hooked agent restored at startup: it redraws, sends `SessionStart` `resume` (or `startup`), redraws again for its window, and refreshes its status line | idle throughout, never working. One whose SessionStart is late shows starting while it draws its first screen, then idle once quiet. `SessionStart` then its prompt (an agent started with its task) is working. | ✓ | S `TestStatusMatrixOwnRedraw` |
| D15 | A pane with no hooks reporting (a shell, a no-hook agent) is resized: a window fitted to it, a window attaching and repainted for (`repaintPane`: a row shorter, then back), a project switched to | a redraw within `resizeEcho` (1 s) of the resize, with nothing typed since, is not work: the pane stays as it was. A resize to the size it already is does nothing at all (`Resize` skips it). Output still coming after the window, or after typing, is working; a pane already working (a build) goes on working. | ✓ | S `TestStatusMatrixOwnRedraw` |
| D16 | A restored workspace: every pane starts and draws its first screen (a shell's prompt, an agent's welcome) | starting, then idle once quiet, never working: output within `startupGrace` (5 s) with nothing typed does not count. `AttentionCount` and `Projects()` report 0 working throughout. A pane whose output streams on past the grace (a shell started on a build) is working. | ✓ | S `TestStatusMatrixOwnRedraw`, W `TestStatusMatrixRestoreAndNavigate` |
| D17 | Switching projects or tabs (`SelectProject`, `SelectTab`) and fitting the project's panes to the window | changes no pane's status, and no count | ✓ | W `TestStatusMatrixRestoreAndNavigate` |

## E. Process lifecycle

| Row | Situation | Expected | ✓? | Test |
|---|---|---|---|---|
| E1 | Process spawned | starting | ✓ | S D1 |
| E2 | Process exits (clean or with an error) | a clean exit shows exited (writing to the pane says so) and is closed by "Close finished panes". A non-zero exit or a kill (not one `Close` asked for) shows **failed** with the exit code as the detail (`Session.ExitFailure`, `Pane.Failed`; sent as `failed` plus `err` on the pane and agent views, drawn `data-status="failed"` in red) and is left for a person, like a pane whose last turn ended in an error. A pane that failed to start already carries `Err`. Phone push reports waiting agents only, not exits. | ✓ | S `TestExitWithAnErrorIsToldFromACleanExit`, W `TestStatusMatrixExitWithAnError`, V `TestStatusMatrixExitWithAnError`, webui `TestAFailedExitIsToldFromACleanOne`, existing `TestExitDeliversFinalOutputThenCloses`, `TestWriteToAnExitedPaneSaysSo` |
| E3 | Failed to start (`Pane.Err`, `Sess == nil`) | reads as exited plus `err`; the web UI overlay shows the error and outcomeOf says "failed" | ✓ | existing `polish_test.go`, `TestFanoutOutcomeMatchesOutcomeOfsThreeKinds` |
| E4 | Restart | new launch id, starting again, and hooks from the old process are dropped (C6) | ✓ | existing `TestALateHookFromBeforeARestartIsDropped` |

## F. Derived views

| Row | Consumer | Rule | ✓? | Test |
|---|---|---|---|---|
| F1 | `AttentionCount` (top bar "▲ N waiting" / "● N working", favicon, title), `Projects()` (rail tiles), `TabActivity(...).NeedsAttention()` (tab badge) | waiting and blocked count as waiting/attention; working counts as working, and so does an idle agent with background work still running (`PaneActivity`); starting, exited and any other idle pane count as neither | ✓ | W `TestStatusMatrixDerivedCounts`, existing `TestProjectCountsSummariseEachStatus` |
| F2 | The same counts for shell panes; `PaneFinished` | a shell that is printing counts as working (a build is work). A shell at its prompt is **not** finished; an exited one is. An idle agent is finished only with BackgroundTasks = 0. | ✓ | W, existing `closefinished_test.go` |
| F3 | `fanoutOutcome` (fan-out history) and the web UI's `outcomeOf` | waiting gives "needs"; blocked gives "failed" ("A tool call was denied: X."); `Err` gives "failed"; idle and exited give "done" | ✓ | V `TestStatusMatrixOutcomeOfEachStatus` |
| F4 | `fanoutOutcome` for a pane closed while **working or starting**, reached through `captureTodoStepOutcome` on any pane close | must not be "done". It is "failed" ("Closed while the agent was still working." or "Closed before the agent had started."), so the step is not ticked. (Fan-out history is gated on `FanoutTabSettled`, so it is unaffected.) | ✓ | V `F4` ×2 |
| F5 | `pushDue` (phone push) | pushes waiting and blocked after the delay; never working, idle, starting or exited | ✓ | V `TestStatusMatrixWhatIsPushed` |
| F6 | `sendAgents` sort (All agents list) | needs-you first: `blocked` ranks with `waiting` in the rank map | ✓ | none |
| F7 | Web UI `projectActivity`, `tabSettleInfo`, `announceStatus`, `notifyAttention`, `outcomeOf` (in `app.js`) | they mirror F1 and F3; `tabSettleInfo` treats everything except working and starting as settled | ✓ | existing webui Go+node tests (`TestTheRailMarkFollowsStatus`, `TestTheRailTileSummarisesTheProjectsActivity`, `TestFanOutHistoryListsPastJobs`, …) |
| F8 | Background tasks in the UI | `BackgroundTasks` is sent as `background` in each pane's view (header and phone) and in the agents list, left out at zero and for an exited pane. The web UI shows "◔ N in background" in the pane header and on the agent's row in the agents list. Alongside it goes `backgroundWork` (`Session.BackgroundWork`, at most 8 entries): each piece's kind, id, the description, command or agent type its start or the Stop list gave (redacted with `record.Redact` and cut to 200 bytes by the hook), when Flockdeck first heard of it, and `listed` when that was a Stop's list rather than its start. The badge's tooltip lists these with "first seen … ago", says "seen since" for a listed one, and "details unavailable (count only)" where Claude Code gave only an id. The pane's status stays idle, but (while any of the work is verified, G7) its header mark is the working mark and its tooltip says it is working in the background; its project's rail badge shows the working mark (below waiting) when the project has only that (`Project.Activity`). | ✓ | V `TestBackgroundWorkReachesTheWindow`, webui `TestAnIdleAgentWithBackgroundWorkSaysSo`, `TestTheBackgroundBadgeListsTheWork`, `TestTheBackgroundWorkCountCanBeRead`; session `TestBackgroundWorkKeepsWhatWasSaidOfIt`; H `TestEmitSaysWhatBackgroundWorkIs` |

## G. Background work: how an end is learned

`Session.background` counts the work an agent has left running after its turn: background shells, background subagents, and anything else Claude Code runs in the background (monitors, workflows). `PaneFinished` needs it at zero before "Close finished panes" or `flockdeck close` will close an idle agent (F2).

The signals below were checked against real hook payloads from Claude Code 2.1.283. A throwaway session with a hook that logged its stdin for every event ran a background shell and a background subagent, with each ending both mid-turn and after the turn. The schema strings in the executable were read as well.

| Row | Signal | What Claude Code sends | Used as | ✓? | Test |
|---|---|---|---|---|---|
| G1 | Start of a background shell | `PostToolUse` Bash with `tool_input.run_in_background: true` and `tool_response.backgroundTaskId: "b8qs3bzx9"` | start `shell:<id>` | ✓ | H |
| G2 | Start and end of a subagent | `SubagentStart` and `SubagentStop` with `agent_id` | start and end `agent:<id>` | ✓ | H |
| G3 | **A background task ending on its own** (a shell exits, or a background subagent finishes) | No hook of its own: `TaskCompleted` is for task-list items, not background tasks. Instead Claude Code submits a `<task-notification>` to the model as a prompt, and that fires **`UserPromptSubmit`** with `prompt` = `<task-notification>\n<task-id>b8qs3bzx9</task-id>…<status>completed</status>…`. It does so both after the turn, where it starts a new turn, and mid-turn, where the notification is folded into the running turn but UserPromptSubmit still fires. | end, by task id, whichever kind it was (`task:<id>`) | ✓ | H, W `B22`, `closefinished_test.go` |
| G4 | **What is still in flight at the end of a turn** | `Stop` carries `background_tasks: [{id, type, status, …}]`, where type is `shell`, `subagent`, `monitor`, `workflow` and so on. The executable documents it as "In-flight background work… Empty array when nothing is in flight". An older Claude Code omits the field. | replaces the count outright (`SetBackground`); an absent field changes nothing | ✓ | H `TestEmitReportsWhatAStopSaysIsStillRunning`, W `B22` |
| G5 | Killed by the model | `PostToolUse` KillShell/TaskStop with the id | end `shell:<id>` | ✓ | H |
| G6 | New conversation | `SessionStart` `clear` or `startup` | forget everything | ✓ | W `B14` |

`SubagentStop` also carries `background_tasks`, but that list still shows the stopping subagent as running, so only Stop's list is trusted.

### G7. Checking idle panes' background work against the stored conversation

`server.backgroundLoop` ticks every 15 s and asks `workspace.DueBackgroundChecks`, which reads no files, which panes are due: Claude Code agents that are idle with background work counted. The first check comes `BackgroundCheckInterval` (45 s) into an idle spell, and the interval doubles up to 8 min while checks find nothing. `RunBackgroundCheck` runs off the workspace goroutine. It finds the conversation file, reads it (the last 8 MB the first time, then only appended whole lines, at most 4 MB per check; what was read is remembered while the pane is open), and stats or tails the per-item files below. Item ids must match `[A-Za-z0-9_-]{1,64}` before they are put into any path.

The shapes below were checked against real Claude Code 2.1.28x files, read for structure only.

| Evidence | Read from | Effect |
|---|---|---|
| `<task-notification>` with `<task-id>` and a stopped `<status>` (`completed`, `stopped`; also `killed`, `failed`, `cancelled`, `error`), as a user turn with `origin.kind: task-notification` or as an `attachment` of type `queued_command` with `commandMode: task-notification` | the conversation | ended, at the line's timestamp |
| hand-back: `origin` with `handback: true` and `senderTaskId` (user turn or `queued_command` attachment) | the conversation | ended |
| `TaskStop`/`KillShell` tool_use whose tool_result is not an error | the conversation | ended, at the result's timestamp |
| last line of `<conversation>/subagents/agent-<id>.jsonl` is a user tool_result with `toolEndsTurn: true` (the subagent's hand-back) | subagent transcript tail | ended |
| subagent transcript modified in the last 2 min | its mtime | confirmed running |
| `<temp>/claude/<project folder>/<conversation>/tasks/<id>.output` non-empty and modified in the last 2 min | its mtime | confirmed running |
| a Monitor's tool_result `{persistent: false, taskId, timeoutMs}`, once `timeoutMs` past the result has gone by | the conversation | ended (strong evidence), dated when the timeout ran out |
| start hook, or named in a Stop's `background_tasks` | hooks | confirmed running |

An end is applied only if its timestamp is after the item's `Since` and after its last running sign (`Session.EndBackgroundWorkSeen`, decided under the session lock). So an old end in the history (a reused task id, a resumed subagent) is ignored, and a Stop list naming the item wins over an end read before it. An Agent call's tool_result is never an end: Claude Code answers every Agent call at once with `status: async_launched`. A drop is recorded with its evidence (`Session.BackgroundEnded`, shown for 2 min, logged with `slog.Debug`).

Work with no running sign for `session.BackgroundUnverifiedGrace` (30 min) is marked `Unverified`. It stays counted, so `PaneFinished` still refuses to close the pane, but `backgroundKeepsAgentWorking` (activity.go) no longer counts the idle agent as working when all of its work is unverified.

Not used:

- the process tree: no command lines on Windows, no table on macOS, and Claude Code's long-lived MCP and helper processes look the same as a running command;
- the pty screen's task bar: the server keeps only the raw byte stream;
- `turn_duration.pendingBackgroundAgentCount`: only at turn end, which Stop already covers.

Still unknowable: a quiet shell or a workflow between Stops, and anything at all on a Claude Code that writes none of the above.

**Residual limitations:**

- The notification (G3) is handed to the model and is not a hook of its own. A task that ends while Claude Code is holding its queue, for example during a permission prompt, is not seen until the notification goes through. The next Stop's list (G4) is the backstop.
- A task stopped from Claude Code's own task list (`/tasks`) while the agent is idle, if that sends no notification, stays counted until the next turn ends.
- A Claude Code older than the `background_tasks` field and the notification prompt falls back to the old behaviour: a shell that ends by itself stays counted until `/clear`, which keeps the pane open rather than closing it by mistake.
- A Stop delivered late (C13), after a notification that followed it, can count a task that has already ended again, until the next Stop.
