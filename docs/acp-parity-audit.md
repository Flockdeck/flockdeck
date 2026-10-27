# ACP parity audit: does the structured pane match the TUIs?

Read-only audit of origin/main at 36114765 (API 1.21, 27 Oct 2026). The question is the claim in [acp-direction.md](acp-direction.md) decision 6 and the [parity matrix](acp-parity-matrix.md) that the structured pane has, or is about to have, parity with the Claude Code, Codex, Gemini and opencode TUIs.

## Method and limits

I read the matrix and the code it describes: `internal/acp/claudeacp`, `internal/acp/codexacp`, `internal/acp/bridge`, `internal/structured`, `ui/src/drawer/Structured.js` and its models, and `docs/structured-pane.md`. I did not run the app, run any test, or start `claude`, `codex`, `gemini` or `opencode`. Every "Claude does X" below is from the adapter source or its doc, not from watching a session. Where a claim depends on what `claude -p` emits or accepts and the repo has no evidence, the cell says "not verified".

The audit is weighted to Claude panes, because the question is whether structured can become the default for them. Codex, Gemini and opencode get a short section at the end.

## Matrix rows that are wrong or stale

The matrix header says it describes 4efb7311. Main has moved nine PRs since, and the matrix was edited unevenly.

| Row | Matrix says | Code says |
| --- | --- | --- |
| Keyboard send | missing: button only | Present. `sendKey` in `ui/src/drawer/composerModel.js:31` is called from the textarea handler (`Structured.js:472`). |
| Run an agent-advertised command | missing | Present as far as the menu goes (T210). The Claude adapter sends `available_commands_update` (`docs/acp-claude-adapter.md`) and a chosen command is sent as prompt text. Whether `claude -p` runs the built-ins (`/compact`, `/clear`, `/context`) is not verified; the adapter doc says "claude runs it" with no evidence. |
| Resume a past session | missing | Built (T189). But Claude's adapter says `loadSession: false`, so a resumed Claude pane has an empty transcript while the agent remembers the conversation (`docs/structured-pane.md:390`). The matrix does not flag this. |
| Approve or reject a plan | partial: "arrives as a permission request and is shown as a card" | Shown as a card titled "Exit plan mode" with no plan text. See ExitPlanMode below. The matrix status overstates it. |
| Image paste | partial | Accurate, with a consequence the matrix understates: both adapters accept `image` blocks (`claudeacp/content.go:61`, `codexacp/content.go:179`) but `Client.PromptBlocks` only builds text blocks (`internal/acp/calls.go:432`), so no agent ever receives a real image block. The agent gets `[image: path]` and has to open the file. |
| Section 8, ten biggest gaps | items 1, 2, 3, 4, 6 listed as open | 1, 2, 3, 4, 6 are built. Items 7, 8 (rewind, fork, clear, new conversation) and the settings surfaces (T215, T217) are not in the tree at all (no match for rewind, fork or a new-session path in `internal/` or `ui/src`). |
| Open question 2 | unverified whether Claude's SDK can set mode, model, interrupt | Answered: `set_permission_mode`, `set_model` and `interrupt` are implemented in the adapter. Effort is not. |

No row marked "present" is plainly false. The ones that read as done but are weaker than the word suggests are: plan approval (above), "Tool calls with output and diffs" (the permission card has no diff, see below), and "Permission prompt: allow once, allow always, reject" (always is session-only and Bash-only for Claude).

## Gap table

Severity is for a daily Claude Code user switching from the TUI. Blocker means they will stop using the structured pane or lose work. High means a daily annoyance or a silent loss. Medium means a workaround exists. Low means rare or cosmetic.

### Approvals and questions

| Feature | TUI behaviour | Flockdeck status | Severity | Suggested task |
| --- | --- | --- | --- | --- |
| Plan mode: approve the plan (ExitPlanMode) | Shows the plan text, offers approve with auto-accept edits, approve with manual approval, or keep planning with feedback | The adapter maps ExitPlanMode to a card titled "Exit plan mode" with kind `switch_mode` and no content (`claudeacp/tools.go:137`). The plan text is in the tool input, which the bridge never keeps (`bridge.go:11`). The card (`bridge.Card`) has only Tool, Kind, Summary, File, Command. A user approves a plan blind. Allow also drops the mode back to `default` behind the user's back (`stream.go:155` passes the change on). | Blocker for anyone who uses plan mode | Put `input.plan` into the card as capped, sanitised markdown; offer "approve and accept edits" as a second option that also sets `acceptEdits`; show the plan in the conversation. |
| AskUserQuestion | Multiple-choice question with options, multi-select, free text | Denied with "Ask the user in plain text instead" (`claudeacp/perm.go:70`). Claude then asks in prose and the user types an answer. `StreamDecision.js` is Flockdeck's own decision card, not this. The Codex adapter has no counterpart for its user-input request (no match in `codexacp`). | High: skills and plan mode use it constantly; the model adapts, but the structured choice is gone | Answer the control request: build a card from `input.questions`, return `updatedInput.answers`. Needs the card type to carry options with labels and descriptions. |
| Edit and write approval with a diff | The permission prompt itself shows the diff | The adapter announces a `tool_call` with a diff before the permission request (`stream.go:260`, `tools.go:90`), so the diff is in the conversation, but the approval card has no diff and the UI links the two only by position. Not verified in a running drawer. A Write over a file larger than 1 MiB shows no diff (`tools.go:20`). | High | Show the same diff inside the card, or move the card next to its tool row. Check the Write-over-large-file case. |
| Always allow | Persists a rule (`Bash(npm test:*)`) to `settings.local.json`, covers a prefix; "allow all edits this session" for edits | Bash and PowerShell only, exact command only, in adapter memory for the life of the session (`perm.go:20`). Every restart or new session asks again. Edits use the `acceptEdits` mode. Pre-existing rules in the user's `settings.json` probably still apply because the adapter passes no `--setting-sources`, not verified. | High (repeated prompts for the same commands each session) | Decide whether to persist rules in the project's `.claude/settings.local.json`; at minimum keep the rules across pane restarts. |
| Reject with a reason | "No, and tell Claude what to do differently" | Reject sends a fixed message (`perm.go:147`); the user's next typed message carries the reason. | Low | None. |
| Permission modes | Shift+Tab cycles default, accept edits, plan, auto/bypass | Selector, `/mode`, mode updates. Modes are default, acceptEdits and plan only (`docs/acp-claude-adapter.md`). No Shift+Tab. No auto or bypass. | Medium (bypass absence is probably right) | Bind Shift+Tab in the composer to the next mode. |
| Permission rules editing (`/permissions`) | List and edit rules | Missing. | Medium | T217. |

### What the agent shows

| Feature | TUI behaviour | Flockdeck status | Severity | Suggested task |
| --- | --- | --- | --- | --- |
| Todo list | Task list above the prompt, Ctrl+T to toggle | The adapter sends TodoWrite as `plan` updates; the bridge keeps them (`bridge.go:586`) and the push carries them; no code under `ui/src` draws them (the drawer greps for plan only in `/mode plan`). Newer Claude builds may use Task tools instead of TodoWrite; not verified, and the adapter's `describeTool` does not know them. | High | Draw the plan rows under the activity line. Check which tool `claude 2.1.311` uses in `-p` mode. |
| Tool call detail, output, expand | Collapsed one-liners, Ctrl+O to expand | Present (tool rows, capped output, `Tool.js`). Tool results cut at 64 KiB in the adapter (`tools.go:189`). | Low | None. |
| Thinking display | Shown dimmed or collapsed, Ctrl+O | Rows exist; no toggle (matrix, not rechecked). | Low | Client toggle. |
| Thinking effort, fast mode | `/effort`, Alt+T, Alt+O | The Claude adapter lists no effort option or fast mode; no match for effort in `claudeacp`. Typing "ultrathink" as prompt text would still be sent, not verified. | Medium | Offer effort as a config option if `claude` lets the adapter set it (flag or control request: not verified). |
| Subagents and background tasks | Agent view, task list, Ctrl+B | A Task/Agent tool is one `think` row; the subagent's inner tool calls are shown, its text is dropped (`stream.go:179`, `l.nested()`). Turns Claude starts on its own (finished background task) block prompts for up to `BusyIdle` = 120 s (`server.go:88`). No list of running background shells (decision: no `terminal` capability). | Medium | Show subagent text collapsed under its row; show "agent busy with a background task" instead of silent waiting. |
| Images the agent returns or reads | Rendered inline in supporting terminals, otherwise a name | Tool results with images become the text `[image]` (`tools.go:207`). | Low | None for now. |
| Large output | Truncated with a hint | Cut at 64 KiB with a notice; bridge caps further. | Low | None. |
| Context meter and cost | `/context`, `/cost`, statusline | Header meter and cost line. The adapter omits cost when `total_cost_usd` is zero (`stream.go:324`), so a subscription user sees "Not reported". No breakdown. | Low | None. |
| Statusline | Custom command output | Not supported; the header meter replaces it. | Low | Document as out of scope. |
| Compaction notice | "Compacting conversation", then a summary line | The Claude adapter handles only `system/init` (`stream.go:116`); `compact_boundary` and status lines are ignored, so auto-compaction is silent. The bridge can show a compaction notice (`state.go:184`) but nothing sends it. | Medium | Map Claude's compact boundary to the ACP compaction update. |
| Errors and limits | "Usage limit reached, resets at 3pm"; "Prompt is too long"; "Overloaded, retrying" | Every failed result collapses to `-32603 "the turn failed"` (`stream.go:353`, `:359`) except an auth failure. A usage limit, context overflow and an API outage all look the same, and the user gets no reset time. This follows the 27 Oct rule that agent error text never reaches the user, but the rule needs a fixed vocabulary, not one phrase. What `claude -p` puts in `result` for a limit is not verified. | High | Classify results into a few fixed outcomes (limit reached, context full, service unavailable, refused) with fixed wording; reset time only if parsed into a time value. |
| Hooks | Hook output and blocks shown in the transcript | Hooks configured in the user's settings probably run inside `claude` (no `--setting-sources` flag) but their output is not shown, and a blocking hook looks like a tool failure. Not verified. | Low | Verify; note in docs. |

### Input

| Feature | TUI behaviour | Flockdeck status | Severity | Suggested task |
| --- | --- | --- | --- | --- |
| Esc to interrupt | Esc stops the turn; Esc Esc opens rewind | No key binding. Esc only closes the slash menu and the `@` list (`Structured.js:442`, `:460`). Stop is a button. | High (a reflex) | Esc in the textarea with a running turn calls the Stop path. |
| Ctrl+C | Interrupt, then twice to quit | Copies text. Interrupt is the button only. | Medium | Same task; leave Ctrl+C for copy when text is selected. |
| Double-Esc / rewind / undo | Restore conversation and files to an earlier prompt | Missing (T215 not started). | High for people who rely on checkpoints; the matrix rates it a top-ten gap | T215; see verdict for the minimal slice. |
| Edit and resend an earlier message | Esc Esc, pick message | Missing. | Medium | With T215. |
| `/clear`, `/new` | New conversation, same pane | No such path. Restart gives a fresh Claude session and keeps the old entries above a notice (`structured-pane.md:111`). The slash menu does show `/clear` if Claude advertises it, but whether `-p` mode honours it is not verified. | High (one of the most used commands) | Add a Flockdeck `/clear` that restarts the pane's session without confirmation, or confirm `claude -p` handles it. |
| `/compact` | Summarise, optional focus text | Passed as prompt text; not verified that `claude -p` runs it. Codex adapter handles it explicitly (`codexacp/content.go:160`). | High until verified | Run it once against a real claude and record the result in the adapter doc. |
| `@file` mentions | Picker; file contents added | Picker inserts `@path` as text; Claude reads the file with its own Read tool. Works, one tool call heavier. | Low | None. |
| `!` shell | Runs a command, output joins the context | Present, but the output joins the conversation only when the user presses "Send to agent". Different from the TUI where it is automatic. | Low | None. |
| Image paste | Ctrl+V, chip in prompt | Paste, drop and button work; the agent receives a path, not an image block (above). | Medium | Send `image` blocks when `promptCapabilities.image` is set. |
| Large paste | `[Pasted text +N lines]` chip | Pasted text goes into the textarea in full; no chip. | Low | None. |
| Prompt history, drafts | Up, Ctrl+R | Up/Down over the last 50; no Ctrl+R. | Low | None. |
| Prompt suggestions | Ghost text for the next prompt | Missing. | Low | None. |
| Vim mode, themes, keybindings | Configurable | Missing; browser textarea. | Low | None. |
| `/` menu | Commands, skills, custom commands | Present. Flockdeck's own entries are `/stop`, `/mode`, `/model`. | Low | None. |

### Session and account

| Feature | TUI behaviour | Flockdeck status | Severity | Suggested task |
| --- | --- | --- | --- | --- |
| Resume and session picker | `--continue`, `/resume` lists past conversations | Only the pane's own saved session id is resumed; no list of other sessions (`session/list` not used). A resumed Claude pane shows an empty transcript (adapter has no `session/load`). Whether Flockdeck keeps the transcript on disk across an app quit is not stated in `structured-pane.md`; not verified. | High: reopening the app shows a blank conversation over a remembering agent | Either replay from Claude's session files (the adapter doc rules this out as unstable) or persist Flockdeck's own entries per pane. |
| Login | In-TUI OAuth | The pane tells the user to run `claude` once in a terminal (matrix section 7). `authMethods` is empty (`server.go:271`). A new user with no terminal habit cannot sign in from Flockdeck. | High for onboarding, none for existing users | Decide: an `authenticate` method that opens the login URL, or keep the manual step and document it in the first-run text. |
| Logout, switch account | `/logout` | Missing. | Low | None. |
| Version and update | Update notice, `claude update` | The adapter targets claude 2.1.311 and drives an undocumented control protocol (`docs/acp-claude-adapter.md`). A newer claude can change it silently; the weekly canary is the only guard, and the user sees "Claude Code is unavailable" or a mis-parsed stream. | High risk for a default | Run the canary on the user's installed version at pane start and show a fixed "version not tested" notice. |
| MCP servers | `/mcp` status, auth, enable | Flockdeck's MCP file is passed with `--mcp-config`; the user's own servers still load. No status, no enable or disable. | Medium | T217. |
| `/config`, `/hooks`, `/skills`, `/agents`, `/memory`, `#` | Edit settings in-app | Missing. | Medium (users can edit files) | T217. |
| `/add-dir` | Add directories | Missing. | Low | None. |
| Desktop and phone notification when a permission is needed | Terminal bell, hooks | The pane status reaches the roster as waiting (`control.go:764` counts attention). I did not trace a system notification for a structured pane. Structured data is refused over the relay, so a phone gets less than a terminal pane gives today. | High for phone users | Trace and test the desktop path; decide the phone path (direction doc, "Open"). |
| Rename, title | `/rename` | Pane rename only. | Low | None. |

### Things Flockdeck terminal panes have that structured panes lose

Not TUI features, but they change on the flip: phone approval and view, the hook-driven status, and the task board (being removed on purpose). Phone approval is the one a daily user will notice.

## Other agents

- Codex: the adapter was built from a route document and a fake app-server and "never run against a real codex" (`acp-direction.md`). It has no effort option, no user-input handling, and a `plan` update path only for `turn/plan/updated`. Not ready to be a default by any standard here.
- Gemini: native ACP; `loadSession: true` (from source, matrix section 10), so resume replays. Hidden entry, never unhidden. Mode and model switching come from the agent. Not verified against a running Gemini.
- opencode: native ACP, beta entry. It has the most protocol coverage and the most e2e tests (`internal/server/opencode_e2e_test.go`). Parity depends on what it advertises; not verified for this audit.

## Verdict

The claim "structured panes have parity with the TUIs" is not true yet. For Claude it is about two-thirds true: prompt, queue, slash menu, model and mode switching, `@file`, images by path, `!`, export, copy and usage all exist and are better than the matrix suggests. The remaining gaps cluster in approvals and in session continuity.

Must be fixed before structured becomes the default for Claude panes:

1. ExitPlanMode approval shows the plan. Today it cannot be approved knowingly.
2. Esc interrupts the turn.
3. Failure outcomes have fixed, distinct wording (usage limit, context full, service down) instead of "the turn failed".
4. Todo or plan rows are drawn.
5. The approval card shows the diff for an edit, or sits beside the tool row that has it.
6. A resumed Claude pane shows its history (persisted Flockdeck entries are the realistic route).
7. `/compact` and `/clear` are verified against a real `claude -p`, and if `/clear` does nothing, Flockdeck provides it.
8. Desktop notification for a waiting structured pane is traced and tested; the phone decision is made.
9. The adapter checks the installed claude version at start, since the control protocol is undocumented.
10. Login: a new user can sign in without a terminal, or the first-run text says exactly what to do.

Can follow:

- AskUserQuestion as a real card (high, but the model degrades to plain text).
- Real image blocks, effort option, Shift+Tab mode cycle, compaction notice, subagent text, persistent allow rules.
- Rewind, edit-and-resend and fork (T215). A minimal version, "restore files to before this prompt" through git, covers most of the use; conversation truncation needs the Flockdeck adapter and is larger.
- Settings surfaces (T217), MCP status, `/add-dir`, logout.
- Vim mode, themes, suggestions, paste chips, statusline: not needed.

Things I could not verify and that change the verdict if wrong: what `claude -p` does with `/compact` and `/clear`; what its `result` line says on a usage limit; whether Claude 2.1.311 uses TodoWrite or Task tools in `-p`; whether hooks and `settings.json` permission rules apply under the adapter; whether the drawer places the permission card next to its tool row; and whether structured transcripts survive an app quit.
