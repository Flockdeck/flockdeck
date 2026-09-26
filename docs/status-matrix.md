# Pane status matrix

This document lists every situation a pane can be in and the status it should show for each one. It also records how that status is signalled today and whether the current code gets it right.

The tests named in the last column walk this table row by row. Each test case carries its row ID, so a failure points at the row that no longer holds.

A row marked **✗** is a confirmed bug. Its test asserts the *correct* behaviour and is skipped through a `knownBug` helper, so CI stays green. To run the known bugs and watch them fail:

```
FLOCKDECK_STATUS_BUGS=1 go test ./internal/session/ ./internal/workspace/ ./internal/server/ -run StatusMatrix
```

When one of these bugs is fixed, delete the `bug:` field or the `knownBug` call from its case, and change ✗ to ✓ here.

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
| `exited` | The process is gone. A pane that failed to start (`Pane.Sess == nil`) also reads as exited. | no |

There is no `failed` status. The web UI derives "failed" itself, from `blocked` or from a start error (`Pane.Err`); see section F.

A status is decided in one of two ways:

1. **Lifecycle events (authoritative).** Claude Code runs `flockdeck hook --event X` for each subscribed event. `hooks.Emit` posts the event to the loopback server. `workspace.handleHook` then applies it in this order:
   1. Launch and session filter.
   2. `NoteBackground`.
   3. `session.StatusForEvent`.
   4. `Session.ResolveBlocked`.
   5. `Session.SetStatusFull`.

   The first event that changes status sets `hooksSeen`. From then on the output-based guesses below are switched off for that pane. The chat client (`internal/chat/lifecycle.go`) posts the same event names through the same `Emit` function.
2. **Output inference (fallback).** This applies to shells, to agents whose Spec reports no lifecycle, and to a hooked agent before its first status-changing event. It reads the pane's output in `Session.publish`:
   - Bytes arriving mean `working`.
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
| A10 | `Notification` `idle_prompt` | no change *when idle*; see C14 for a pane still showing working | ignored (`ok=false`) | ✓ / see C14 | S, H |
| A11 | `Notification` `elicitation_dialog`, `agent_needs_input`, unknown types, or the chat client naming a tool | waiting | `StatusForEvent` | ✓ | S, H |
| A12 | `Notification` with no type (Claude Code older than 2.1.269) | waiting | 〃 | ✓ on an idle pane (see B17) | S, H |
| A13 | `Notification` `auth_success`, `elicitation_complete`, `elicitation_response`, `agent_completed`, `computer_use_exit` | not reported at all | dropped in `Emit` (`finishedNotifications`) | ✓ | H |
| A14 | `Stop` | idle, or blocked (see B9) | `StatusForEvent` + `ResolveBlocked` | ✓ | S, H |
| A15 | `StopFailure` (the turn ended on an API error) | idle, or blocked after a denial | 〃 | ✓ | S, H |
| A16 | `SessionStart` (any source) | no status change: it also fires mid-turn on a compaction | ignored; source drives background bookkeeping only | ✓ | S |
| A17 | `SessionEnd` | no status change: `/clear` fires it and carries on, and a real exit is seen by the PTY reader | ignored | ✓ | S, and existing `TestNoLifecycleEventCanMarkALivePaneExited` |
| A18 | `SubagentStart` / `SubagentStop` | no status change of their own; they count background work | `NoteBackground` only | ✓ (but see B21) | S |
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
| B7 | Permission prompt refused with **Esc**, which is Claude Code's own "No, and tell Claude what to do differently (esc)" | working, then idle once quiet (same as B5) | only Enter (`\r`) counts as an answer, so the pane **stays amber at an empty prompt** until the next UserPromptSubmit | **✗** | S `B7` |
| B8 | AskUserQuestion | waiting(AskUserQuestion). Its nudge keeps it waiting. Enter does not clear it, because Enter moves between questions. Its PostToolUse means working, and Stop means idle. | PreToolUse special case; `toolQuestion` stays false | ✓ | S |
| B9 | Denial: PermissionDenied, then the turn ends | **blocked**, naming the tool (reported once) | `ResolveBlocked`. It is cleared by a successful PostToolUse or a new UserPromptSubmit, and an ordinary PostToolUseFailure does not set it. | ✓ | S, W, existing `blocked_test.go` |
| B10 | The turn ends on an API error (StopFailure) | idle, or blocked if a denial is outstanding | 〃 | ✓ | S |
| B11 | The user stops a running tool (Esc or Ctrl+C) | idle. It is not blocked even after a denial, because the user is already at the pane. | `is_interrupt` gives `Interrupted` | ✓ | S, W |
| B12 | The user presses **Esc while the model is streaming text** (no tool running) | idle | Claude Code fires **no Stop** ("Stop does not run on a user interrupt") and no PostToolUseFailure. Nothing arrives, so the pane **stays working forever**. The `idle_prompt` Notification about 60 s later is the only signal, and it is ignored. | **✗** | S `B12` |
| B13 | Auto-compaction mid-turn (`SessionStart` source `compact`) | stays working; background work is kept | SessionStart is ignored for status | ✓ | S, W |
| B14 | `/clear` at the prompt (SessionEnd, then SessionStart `clear`) | stays idle; background work is forgotten; the conversation id follows | `handleHook` | ✓ | S, W, existing `clear_test.go` |
| B15 | Resume (`SessionStart` `resume`) | no change; background work is kept | 〃 | ✓ | W |
| B16 | The `idle_prompt` nudge arrives after a turn ended | stays idle: never amber, never pushed | ignored | ✓ | S, W, existing `TestIdleReminderIsNeverPushed` |
| B17 | Claude Code older than 2.1.269 sends an **untyped** idle nudge after a turn ended | idle | an untyped Notification with no tool named, landing on a pane the hooks have left idle with no background work counted, is ignored (`Session.IsStaleNudge`). A real ask comes mid-turn, when the pane is already working or waiting, so it is not affected. `PermissionRequest` cannot be the tell: an older Claude Code does not send it. | ✓ | S, W |
| B18 | A repeated nudge for the same wait | no change report, and the wait clock is not restarted | `SetStatusFull` dedupe | ✓ | S `TestStatusMatrixWaitClock` |
| B19 | Manual `/compact` typed at an idle prompt | working while it compacts, idle when done | `PreCompact` (trigger `manual`) shows working; `PostCompact`, or the `SessionStart` `compact` that follows it on a Claude Code without that event, ends it (`Session.CompactionStatus`). An automatic compaction says nothing, so B13 is untouched. A quiet fallback (`SettleWhenQuiet`) covers a compaction that never reports its end. Both events are subscribed only from 2.1.269 (`laterHookEvents`); an older one still shows idle. | ✓ | S, W |
| B20 | Foreground subagent (Task): its tool calls fire hooks under the pane's session | working until the parent turn's Stop, then idle, with nothing left counted | Pre/Post events plus SubagentStart/Stop bookkeeping | ✓ | S, W |
| B21 | **Background subagent** that is still working after the parent turn's Stop | idle, with BackgroundTasks > 0; idle, with 0, once its SubagentStop arrives | The subagent's own PreToolUse and PostToolUse arrive after the Stop and mark the pane **working**. Its SubagentStop only decrements the count, so the pane **stays working with nothing running**. | **✗** | S `B21`, W `B21` |
| B22 | Background shell (`run_in_background` Bash) | idle once the turn ends, with BackgroundTasks = 1 until KillShell or `/clear` (Claude Code fires nothing when it simply ends) | `backgroundOf` | ✓ | S, W, existing `closefinished_test.go` |
| B23 | Flockdeck chat client turns | same as B1, B2, B4 and B5: working, waiting(tool) at a question, working again whether allowed or refused, idle at the end (also after an interrupt, and after an API key is found) | `reporter` sends events synchronously, in order, from one goroutine | ✓ | G `TestStatusMatrixChatTurns`, existing `declinedstatus_test.go` |

## C. Ordering, races and lost events

The hypothesis was: "a PostToolUse or PreToolUse for the turn's last tool call can arrive after the turn's Stop and flip the pane to working with nothing to flip it back."

**Verdict: confirmed, with a narrower trigger than suspected.**

- **What happens today.** Nothing in `handleHook` has any notion of a turn or of event order. A late event is applied like any other. Once `hooksSeen` is set, no timer or output rule can return a `working` pane to `idle`. So *if* a tool event lands after its Stop, the pane stays working until the next prompt (C1, C2).
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
| C1 | **Late PostToolUse** (or PostToolUseFailure) for the last tool, delivered after the turn's Stop | idle. A Post event with no PreToolUse since the last Stop belongs to the finished turn and must be ignored. (A background subagent's Post always follows its own post-Stop Pre, so this rule does not misfire on B21.) | applied, giving **working forever** | **✗** | S `C1` ×2, W `C1` |
| C2 | **Late PreToolUse** delivered after the Stop ("Stop before PreToolUse") | Cannot be told from a background subagent's PreToolUse at arrival. It must still **recover**: the `idle_prompt` that follows means idle (see C14). | working, and nothing recovers it | **✗** | S `C2` |
| C3 | Duplicate Stop | idle, no change report | dedupe | ✓ | S |
| C4 | Duplicate Stop after a denial | stays **blocked** until something new happens (a prompt or a successful tool) | `ResolveBlocked` spends the mark on the first Stop, so the second Stop gives **idle** and the blocked marker, phone push and tab badge disappear | **✗** (minor) | S `C4` |
| C5 | A `permission_prompt` Notification arriving after the Stop | waiting. Accepted, because a background subagent can legitimately ask for permission after the parent's Stop. | waiting | ~ | S |
| C6 | An event carrying a stale `Launch` (from the process before a restart) | dropped | `handleHook` launch check | ✓ | W `TestStatusMatrixHookRouting`, existing `latehook_test.go` |
| C7 | An event with no `Launch` (a hook older than the launch check) | applied to the pane | 〃 | ✓ | W |
| C8 | An event for an unknown session id, or for one pane among several | dropped, or applied to that pane only | `w.panes` lookup | ✓ | W |
| C9 | An event for a pane with no session (start failed, or mid-restart) | dropped | `sess == nil` check | ✓ | W |
| C10 | Any event after the process exited | stays exited | `SetStatusFull` refuses to leave `exited` | ✓ | S `TestStatusMatrixExited`, W `TestStatusMatrixAfterExit` |
| C11 | SessionEnd while the process is still alive | no change; only the PTY reader's exit gives `exited` | A17 | ✓ | S |
| C12 | Hook cannot reach Flockdeck (not running, or connection refused) | the hook fails fast and silently (stderr only) and never blocks the agent; the event is lost | `Emit` returns an error; `hook` prints it to stderr | ✓ | existing `TestEmitToDeadServerIsNotFatal` |
| C13 | Hook times out (Flockdeck slow; hook slow to start) | the agent is not held up; *ideally* the late event is not applied out of order | `Emit` gives up after 3 s, but the server still applies the event later, after newer ones | mechanism for C1, C2 and C5 | H `TestStatusMatrixAHookThatGaveUpLandsAfterTheNextOne` |
| C14 | **Missed Stop** (C12, C13, B12) | idle once Claude Code's `idle_prompt` arrives (about 60 s after it returns to its prompt). The nudge must turn a *working* pane idle, while leaving waiting and blocked panes alone. | `idle_prompt` is ignored, so the pane shows **working forever** | **✗** | S `C14`, W `C14` |
| C15 | Missed UserPromptSubmit | working from the first evidence of the turn: a tool event at once; for a turn with no tools, Enter at the idle prompt followed by output that keeps drawing (`Session.guessTurn`). The Stop ends it, and silence does if no hook ever comes. | Enter that draws once and stops (a slash command, an empty line) is not taken for a turn. | ✓ | S |
| C16 | Hook payload unreadable (bad JSON on stdin) | the event is still reported, without tool detail | `Emit` ignores decode errors | ✓ | H |
| C17 | PermissionRequest answered before the 6 s Notification, which then arrives anyway | working. A stale nudge turns the pane amber again until its PostToolUse. | amber until PostToolUse | ~ self-heals | none (same class as C13) |
| C18 | A denial, then a new conversation (`SessionStart` from `/clear`), then a Stop with no prompt in between | idle: `ResolveBlocked` documents that SessionStart clears the mark | `StatusForEvent` does not map SessionStart, so `handleHook` returns before `ResolveBlocked`. That clearing path is dead code and the mark survives. | **✗** (latent, low impact: the next UserPromptSubmit also clears it) | S `C18`, W `C18` |

**Recommended fix direction** (not part of this change):

1. Treat `idle_prompt` as idle when the pane shows `working`. This fixes B12, C2 and C14, and B21's tail, with one line in `StatusForEvent` or `handleHook` and no new timing.
2. Keep a per-session "turn open" flag: set it on UserPromptSubmit or on PreToolUse after a Stop, and clear it on Stop, StopFailure or Interrupted. Drop a Post event while the turn is closed and no PreToolUse has opened it again. This fixes C1.
3. Let SubagentStop return a closed-turn pane to idle. This fixes B21.
4. Treat a lone `\x1b` like `\r` for a hook-reported `toolQuestion`. This fixes B7.
5. Leave `blockedTool` set until something new clears it, and run `ResolveBlocked` for SessionStart. This fixes C4 and C18.

## D. Output inference (no lifecycle hooks)

| Row | Situation | Expected | ✓? | Test |
|---|---|---|---|---|
| D1 | A pane starts | starting | ✓ | S `TestStatusMatrixOutputFallback` |
| D2 | It prints output, then goes quiet for 3 s | working, then idle | ✓ | S, existing `TestOutputMarksAPaneWorkingUntilItGoesQuiet` |
| D3 | It prints steadily | working; the busy clock is not restarted | ✓ | existing (same test) |
| D4 | An agent rings the bell after startup (5 s, or once typed into) | waiting; a bell during startup is ignored | ✓ | S, existing `TestBellWhileStartingUpIsNotAttention`, `TestBellReachesAPaneNobodyHasTypedInto` |
| D5 | A shell rings the bell | never waiting | ✓ | S |
| D6 | Typing answers an inferred wait | working, then idle once quiet | ✓ | existing `TestAnsweringAPaneClearsAnInferredWait` |
| D7 | Focus, mouse and terminal-reply sequences | not an answer | ✓ | existing `TestATerminalsRepliesAreNotAnAnswer`, `TestScrollingIsNotAnswering` |
| D8 | Output while waiting | stays waiting | ✓ | S, existing `TestOutputDoesNotOverruleTheBell` |
| D9 | Once a status-changing hook has arrived | output and bells are ignored, and a hooked `working` never settles on quiet (this is the flip side of C14) | ✓ | S, existing `TestOutputDoesNotOverruleALifecycleHook`, `TestBellIsIgnoredOnceHooksReport` |
| D10 | Spec patterns (for agents without hooks) | the patterns decide waiting or idle; an answered question is not re-read | ✓ | existing `TestPatternsSharpenTheFallback`, `TestAnAnsweredQuestionIsNotAskedAgain` |
| D11 | Exited | stays exited whatever else is printed | ✓ | S |
| D12 | A Claude pane that has only sent SessionStart (no prompt yet) | still read from output: working while it draws, idle once quiet | ✓ | S |
| D13 | Jev assist for an agent without hooks that went quiet | may re-classify an idle guess as waiting | ✓ | existing `assist_test.go` |

## E. Process lifecycle

| Row | Situation | Expected | ✓? | Test |
|---|---|---|---|---|
| E1 | Process spawned | starting | ✓ | S D1 |
| E2 | Process exits (clean or with an error) | exited; writing to the pane says so. **Neither the UI nor the protocol distinguishes an exit with an error (`ExitErr`) from a clean one.** | ✓ / ~ design gap | existing `TestExitDeliversFinalOutputThenCloses`, `TestWriteToAnExitedPaneSaysSo` |
| E3 | Failed to start (`Pane.Err`, `Sess == nil`) | reads as exited plus `err`; the web UI overlay shows the error and outcomeOf says "failed" | ✓ | existing `polish_test.go`, `TestFanoutOutcomeMatchesOutcomeOfsThreeKinds` |
| E4 | Restart | new launch id, starting again, and hooks from the old process are dropped (C6) | ✓ | existing `TestALateHookFromBeforeARestartIsDropped` |

## F. Derived views

| Row | Consumer | Rule | ✓? | Test |
|---|---|---|---|---|
| F1 | `AttentionCount` (top bar "▲ N waiting" / "● N working", favicon, title), `Projects()` (rail tiles), `TabNeedsAttention` (tab badge) | waiting and blocked count as waiting/attention; working counts as working; starting, idle and exited count as neither | ✓ | W `TestStatusMatrixDerivedCounts`, existing `TestProjectCountsSummariseEachStatus` |
| F2 | The same counts for shell panes; `PaneFinished` | a shell that is printing counts as working (a build is work). A shell at its prompt is **not** finished; an exited one is. An idle agent is finished only with BackgroundTasks = 0. | ✓ | W, existing `closefinished_test.go` |
| F3 | `fanoutOutcome` (fan-out history) and the web UI's `outcomeOf` | waiting gives "needs"; blocked gives "failed" ("A tool call was denied: X."); `Err` gives "failed"; idle and exited give "done" | ✓ | V `TestStatusMatrixOutcomeOfEachStatus` |
| F4 | `fanoutOutcome` for a pane closed while **working or starting**, reached through `captureTodoStepOutcome` on any pane close | must not be "done". Today it is "done", so closing a todo step's pane mid-turn **records the attempt as done and ticks the step**. (Fan-out history is gated on `FanoutTabSettled`, so it is unaffected.) | **✗** | V `F4` ×2 |
| F5 | `pushDue` (phone push) | pushes waiting and blocked after the delay; never working, idle, starting or exited | ✓ | V `TestStatusMatrixWhatIsPushed` |
| F6 | `sendAgents` sort (All agents list) | needs-you first. `blocked` is missing from the rank map, so it defaults to 0 and sorts with waiting: correct by accident. | ✓ (fragile) | none |
| F7 | Web UI `projectActivity`, `tabSettleInfo`, `announceStatus`, `notifyAttention`, `outcomeOf` (in `app.js`) | they mirror F1 and F3; `tabSettleInfo` treats everything except working and starting as settled | ✓ | existing webui Go+node tests (`TestTheRailMarkFollowsStatus`, `TestTheRailTileSummarisesTheProjectsActivity`, `TestFanOutHistoryListsPastJobs`, …) |
| F8 | Background tasks in the UI | `BackgroundTasks` is used only by `PaneFinished`. It is not sent to the UI or the phone, so "idle with a background subagent" looks like plain idle. | ~ design gap | none |
