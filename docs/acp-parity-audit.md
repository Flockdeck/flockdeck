# ACP parity audit: does the structured pane match the TUIs?

First written on 27 Oct 2026 against origin/main 36114765. Refreshed on 28 Oct 2026 against origin/main 1be474cd (through #730). The question is the claim in [acp-direction.md](acp-direction.md) decision 6 and the [parity matrix](acp-parity-matrix.md) that the structured pane has, or is about to have, parity with the Claude Code, Codex, Gemini and opencode TUIs.

## What is still open

The verdict is in the last section. This is the short version for deciding whether new Claude panes can default to structured. In order:

1. Phone approval. A structured pane that waits for a permission or a question gets a desktop notification but no phone push, and the relay refuses structured data. A phone sees the pane as "waiting" in its list and cannot answer. Terminal panes do both today. This is a product decision, not a bug (#378).
2. Allow always does not last. It covers shell commands only, exact command only, and lives in the adapter process, so every pane restart asks again. Persisting rules is not designed.
3. Files cannot be rewound. Conversation rewind and fork work for Claude and Codex panes (#730), but nothing puts changed files back, and Esc Esc is not bound. People who lean on checkpoints will notice.
4. Four Claude paths have never run against a real claude: the AskUserQuestion answer format, the whole rewind and fork path, a real usage-limit result, and which todo tool current claude uses in `-p` mode (the plan checklist handles TodoWrite only).
5. Codex is not ready to be a default. It has only ever run against a fake app-server: no history on resume, no question card, no effort option, sign-in and rewind untested. Gemini and opencode were not checked at all in this refresh.
6. Smaller gaps: images reach the agent as a file path, not an image block; no Shift+Tab mode cycle or effort option; "approve and accept edits" on plan approval; a subagent's text is dropped; no "New conversation" button; no session picker.

Everything in the ten must-fix items below is built for Claude except item 8 (desktop done, phone undecided) and item 10 (sign-in works for the link, not checked to completion).

## What is checked against what

Merged tests exercise the adapters, the bridge and the drawer models. Almost all of that is against fakes, and the docs say so. What was seen with a real binary:

- Real claude 2.1.311 and 2.1.312, by hand: the ExitPlanMode request shape (captured in plan mode), `/compact`, `/context` and `/clear` under `claude -p`, a signed-out prompt refused as authentication required, the `claude auth login` address printed on stdout when piped (no sign-in completed), and the claude flags a rewind and fork depend on (`--resume-session-at`, `--fork-session`).
- Not real, fake claude only: AskUserQuestion answers, the rewind and fork path through the adapter and the drawer, the failure classes (shapes taken from the claude binary's strings, no real usage limit provoked), the version check, transcript replay beyond what was read from real session files.
- Codex: nothing is checked against a real codex. Everything, including sign-in (`codex login`), rewind (`thread/rollback`, `thread/fork`) and the turn count `kN`, is against a fake app-server and a route document. `docs/acp-codex-adapter.md` lists what is unverified.
- Gemini: read from source and a live-agent test that runs only when Gemini is installed (`internal/acp/live_agents_test.go`); not run for this refresh. Rewind and fork are not offered for it.
- opencode: has the most end-to-end tests (`internal/server/opencode_e2e_test.go`); parity depends on what it advertises, not verified here.
- UI: the drawer models (interrupt keys, notifications, plan checklist) have JS unit tests. I did not run the app or the tests for this refresh, so "done" for those rows means the code and its tests exist on main.

## Method and limits

I read the matrix and the code it describes on main: `internal/acp/claudeacp`, `internal/acp/codexacp`, `internal/acp/bridge`, `internal/structured`, `internal/server/push.go`, `ui/src/drawer/*`, `ui/src/lib/attention.js`, the tests next to them and the adapter docs. I started no agent and ran no test. Status words: done (with the PR), partial (with what is missing), open.

The audit is weighted to Claude panes, because the question is whether structured can become the default for them.

## Matrix rows that were wrong or stale

The first version of this audit found the rows in the left column stale. All of them are now corrected in the matrix.

| Row | Status on 1be474cd |
| --- | --- |
| Keyboard send | Done (#716). Enter sends, Shift+Enter is a newline. |
| Run an agent-advertised command | Done (T210). `/compact`, `/context` and `/clear` run under `claude -p` 2.1.312. |
| Resume a past session | Claude done (#728): the pane shows its earlier conversation, replayed from claude's session file. Codex open: `loadSession` is false and the pane opens empty. No session picker. |
| Approve or reject a plan | Done (#726): the card shows the plan text. Partial: no "approve and accept edits". |
| Image paste | Partial (#716). Both adapters accept `image` blocks, but `Client.PromptBlocks` builds text blocks only (`internal/acp/calls.go`), so the agent gets `[image: path]`. |
| Matrix section 8, ten biggest gaps | Rewritten with a status per item. |
| Open question 2 (can Claude set mode, model, interrupt) | Answered: the adapter does all three. Effort is not offered. |

## Gap table

Severity is for a daily Claude Code user switching from the TUI. Blocker means they will stop using the structured pane or lose work. High means a daily annoyance or a silent loss. Medium means a workaround exists. Low means rare or cosmetic. The status column is on 1be474cd.

### Approvals and questions

| Feature | TUI behaviour | Status | Severity | What is left |
| --- | --- | --- | --- | --- |
| Plan mode: approve the plan (ExitPlanMode) | Shows the plan text; approve with auto-accept edits, approve with manual approval, or keep planning with feedback | Done (#726). The card shows the plan as text (capped at 32 KiB on the card); a request with no plan text is denied with no card, so a plan is never approved unseen. The request shape was captured from claude 2.1.312. Partial: Allow is a plain allow, and the mode drops back to `default` afterwards; "approve and accept edits" is not built. | Was blocker; now medium | The second option, which also sets `acceptEdits` (`docs/acp-claude-adapter.md`). |
| AskUserQuestion | Multiple-choice question with options, multi-select, free text | Claude done (#726): a question card with options, multi-select and free text; answers go back as `updatedInput.answers`. The answer format comes from Claude Code's documented tool input and was never run against a real claude. Codex: no counterpart. | High, now mostly closed | Run it once against a real claude. |
| Edit and write approval with a diff | The permission prompt shows the diff | Done (#726): the card carries the diff. A Write over a file larger than 1 MiB shows no diff, as before. Not seen in a running drawer. | Was high | None. |
| Always allow | Persists a rule to `settings.local.json`, covers a prefix; "allow all edits this session" | Open. Bash and PowerShell only, exact command only, adapter memory for the life of the session (`claudeacp/perm.go`). Every restart asks again. Whether the user's `settings.json` rules apply under the adapter is not verified. | High | Decide whether to persist rules; at minimum keep them across pane restarts. |
| Reject with a reason | "No, and tell Claude what to do differently" | Open, low: reject sends a fixed message; the next typed message carries the reason. | Low | None. |
| Permission modes | Shift+Tab cycles default, accept edits, plan, auto/bypass | Partial (#712): selector, `/mode`, mode updates; default, acceptEdits and plan only. No Shift+Tab, no auto or bypass. | Medium | Bind Shift+Tab to the next mode. |
| Permission rules editing (`/permissions`) | List and edit rules | Open. Decided in T217 as a later task. | Medium | Needs an extension method on both adapters. |

### What the agent shows

| Feature | TUI behaviour | Status | Severity | What is left |
| --- | --- | --- | --- | --- |
| Todo list | Task list above the prompt, Ctrl+T to toggle | Done for TodoWrite (#723): a collapsible checklist above the conversation (`TodoList.js`); click the head, no Ctrl+T. Which tool current claude uses in `-p` mode is not checked, and `describeTool` knows TodoWrite only. | Was high | Check the tool in `-p` mode on a current claude. |
| Tool call detail, output, expand | Collapsed one-liners, Ctrl+O to expand | Done. Tool results are cut at 64 KiB in the adapter. | Low | None. |
| Thinking display | Dimmed or collapsed, Ctrl+O | Partial: rows show; I found no toggle. | Low | Client toggle. |
| Thinking effort, fast mode | `/effort`, Alt+T, Alt+O | Open. Neither own adapter lists an effort option. | Medium | Offer effort if claude lets the adapter set it (not checked). |
| Subagents and background tasks | Agent view, task list, Ctrl+B | Open. A Task/Agent tool is one row; the subagent's tool calls show and its text is dropped. A turn claude starts on its own blocks prompts for up to `BusyIdle`, 120 s (`server.go`). No list of background shells (no `terminal` capability by decision). | Medium | Show subagent text; show "busy with a background task". |
| Images the agent returns or reads | Inline in supporting terminals | Open, low: results with images become the text `[image]`. | Low | None for now. |
| Large output | Truncated with a hint | Done: cut at 64 KiB with a notice; the bridge caps further. | Low | None. |
| Context meter and cost | `/context`, `/cost`, statusline | Done (#720): header meter, detail with used, window, left and cost. The adapter omits cost when `total_cost_usd` is zero, so a subscription user sees "Not reported". No breakdown. | Low | None. |
| Statusline | Custom command output | Out of scope; the header meter replaces it. | Low | Document as out of scope. |
| Compaction notice | "Compacting conversation", then a summary line | Done (#724): claude's `compact_boundary` line becomes "Context compacted: N to M tokens." for `/compact` and automatic compaction. The "compacting" status line is not shown. | Done | None. |
| Errors and limits | "Usage limit reached, resets at 3pm"; "Prompt is too long"; "Overloaded, retrying" | Done, except retry visibility (#724). Failures are classed (usage limit, context full, authentication, outage, refused, other) with fixed text per class (`internal/structured/failure.go`). The reset time is shown only when it is a Unix time within 31 days. A real usage limit and full context were never provoked. claude's `api_retry` lines are not read, so a retry loop is silent until it fails as an outage. | Low | A retry notice if wanted. |
| Hooks | Hook output and blocks in the transcript | Open, not verified. Hooks probably run inside `claude` but their output is not shown, and a blocking hook looks like a tool failure. | Low | Verify; note in docs. |

### Input

| Feature | TUI behaviour | Status | Severity | What is left |
| --- | --- | --- | --- | --- |
| Esc to interrupt | Esc stops the turn; Esc Esc opens rewind | Done (#723). Esc in the box (once empty) or anywhere in the drawer stops a running turn and keeps the queue; with a permission request open Esc is the card's deny. | Was high | None. |
| Ctrl+C | Interrupt, then twice to quit | Done (#723): interrupts when no text is selected, copies otherwise. | Was medium | None. |
| Double-Esc / rewind / undo | Restore conversation and files to an earlier prompt | Partial (#730). `/rewind` takes the conversation back for Claude and Codex panes, with a confirm. Files are not put back (the confirm text says so). Esc Esc, `/undo`, `/redo`, `/restore` are not built. | High for checkpoint users | File rewind: a git-based "restore files to before this prompt" is the small slice. |
| Edit and resend an earlier message | Esc Esc, pick message | Partial (#730): `/rewind` puts the removed prompt's text in the prompt box. No edit in place. | Low | None. |
| Fork | `/branch`, `/fork` | Done (#730) for Claude and Codex: a new pane beside the original, at most 4 forks per pane. It shares the directory and the notice says so. Codex fake only. | Low | None. |
| `/clear`, `/new` | New conversation, same pane | Done for Claude (#724, #725). `/clear` runs under `claude -p`, the pane shows "Started a new conversation." and saves claude's new session id. `/new` is not a claude command. No Flockdeck "New conversation" button. Rewind and fork are refused after a `/clear`. | Was high | A button, if wanted. |
| `/compact` | Summarise, optional focus text | Done for Claude (#724): runs under `claude -p` 2.1.312 and shows the compaction notice. | Done | None. |
| `@file` mentions | Picker; file contents added | Done as text (#716): the picker inserts `@path`; Claude reads the file with its own tool. | Low | None. |
| `!` shell | Runs a command, output joins the context | Done (#721). Output joins the conversation only when the person presses "Send to agent". | Low | None. |
| Image paste | Ctrl+V, chip in prompt | Partial (#716): paste, drop and button work; the agent receives a path, not an image block. | Medium | Send `image` blocks when `promptCapabilities.image` is set. |
| Large paste | `[Pasted text +N lines]` chip | Open: the text goes in full. | Low | None. |
| Prompt history, drafts | Up, Ctrl+R | Partial (#716): Up/Down over the last 50, drafts kept per pane; no Ctrl+R. | Low | None. |
| Prompt suggestions, Vim mode, themes | Ghost text, configurable | Open, not wanted. | Low | None. |
| `/` menu | Commands, skills, custom commands | Done (T210). Own entries: `/stop`, `/mode`, `/model`, `/info`, `/rewind`, `/fork`. | Low | None. |
| Queue | Type while the agent works | Done (#719): a list under the conversation; edit, remove, send now, clear. | Low | None. |
| Export, copy | `/export`, `/copy` | Done (#720). Markdown export to a folder the person names, desk only; no JSON. | Low | None. |

### Session and account

| Feature | TUI behaviour | Status | Severity | What is left |
| --- | --- | --- | --- | --- |
| Resume and session picker | `--continue`, `/resume` lists past conversations | Claude done (#728): a restored pane shows its earlier conversation (newest 2000 entries) from claude's session file; if the file is gone it shows nothing. Codex open: no load, the pane opens empty. No picker: `session/list` is unused. | Claude done; Codex open | Codex: `thread/turns/list` once its result shape is seen in a real codex. |
| Login | In-TUI OAuth | Partial (#727, #729). A pane that needs a login opens Agent info; its Sign in button runs `claude auth login` (or `codex login`) as a process of the adapter and shows the address it prints as a link, validated (https, host list, no userinfo or port, 2048 bytes); the pane restarts when the command exits 0. Checked live on claude 2.1.312 with piped output and no sign-in completed: address on stdout in 0.2 s. A completed sign-in was not seen. `codex login` was not checked at all (codex was not installed). | Low | Complete one sign-in on claude; check `codex login`. |
| Logout, switch account | `/logout` | Open. The client has no `logout` call. | Low | None. |
| Version and update | Update notice, `claude update` | Version check done (#724): a major.minor other than the manifest's `testedWith` series still runs, with a fixed notice; a failed `initialize` handshake fails closed as "Claude Code is unavailable". No update notice. | Low | None. |
| MCP servers | `/mcp` status, auth, enable | Partial: Flockdeck's MCP file is passed with `--mcp-config`, the user's own servers still load, and Agent info lists names (#727). No status or enable; ACP has no method. | Medium | T217. |
| `/config`, `/hooks`, `/skills`, `/agents`, `/memory`, `#` | Edit settings in-app | Open by decision (T217): the agent's own files; mode, model and options are in the header. | Medium | None planned. |
| `/add-dir` | Add directories | Open. | Low | None. |
| Desktop notification when a permission is needed or a turn ends | Terminal bell, hooks | Done on the desktop (#723), unit-tested only. A structured pane that asks for a permission becomes "waiting" and a pane that needs setup becomes "blocked"; each raises "NAME needs you" while the window is behind another (`ui/src/lib/attention.js`, `waitsOf` and `noteOf`). A structured turn that ran ten seconds or more and went from working to idle raises "NAME finished". The notification carries the pane's name and branch and nothing the agent said. Not run in the real desktop app. | Was high | Run it once in the app. |
| Phone notification and approval | Terminal panes have both | Open, undecided. `pushDue` skips a pane with no session (`internal/server/push.go`), so a waiting structured pane sends no push (decided in #378 on the grounds that the phone could not answer). Structured data is refused over the relay. The phone sees the roster status "waiting" and the pane's name, nothing more. | High for phone users | Product decision: push a fixed "needs you" message, or accept the loss. |
| Rename, title | `/rename` | Partial: pane rename only. | Low | None. |

### Things Flockdeck terminal panes have that structured panes lose

Phone approval and view, the hook-driven status, and the task board (being removed on purpose). Phone approval is the one a daily user will notice.

## Other agents

- Codex: the adapter was built from a route document and a fake app-server and "never run against a real codex" (`acp-direction.md`). It has no effort option, no question handling, no history on resume, and a `plan` update path only for `turn/plan/updated`. Rewind, fork and sign-in exist and are fake only. Not ready to be a default by any standard here.
- Gemini: native ACP; `loadSession: true` (from source), so resume should replay. Hidden entry, never unhidden. Mode and model switching come from the agent. Not run against a real Gemini.
- opencode: native ACP, beta entry, most protocol coverage and most e2e tests. Not rechecked.

## Verdict

The claim "structured panes have parity with the TUIs" is closer to true for Claude than on 27 Oct, and still not true. Prompt, queue, slash menu, model and mode switching, `@file`, `!`, export, copy, usage, interrupt keys, plan checklist, plan and diff on cards, question cards, failure wording, compaction and clear notices, resume with history, sign-in link, and conversation rewind and fork all exist on main. The blockers are in the list at the top: phone approval, allow-always that does not last, no file rewind, and four Claude paths never run for real.

Must fix before structured becomes the default for Claude panes, as listed on 27 Oct, with the status on 28 Oct:

1. ExitPlanMode approval shows the plan. Done (#726). Remaining: "approve and accept edits".
2. Esc interrupts the turn. Done (#723).
3. Failure outcomes have fixed, distinct wording. Done (#724).
4. Todo or plan rows are drawn. Done for TodoWrite (#723); the tool in current claude's `-p` mode is unchecked.
5. The approval card shows the diff for an edit. Done (#726).
6. A resumed Claude pane shows its history. Done (#728).
7. `/compact` and `/clear` work and the saved session follows a `/clear`. Done (#724, #725), checked on claude 2.1.312.
8. Desktop notification for a waiting structured pane is traced and tested; the phone decision is made. Partial: the desktop path is traced and unit-tested (#723), the phone decision is not made (it is "no push" by default).
9. The adapter checks the installed claude version at start. Done (#724).
10. A new user can sign in without a terminal. Partial (#727, #729): the link is checked live on claude, a completed sign-in was not seen, codex is unchecked; the manual step stays as fallback.

Can follow, with status:

- AskUserQuestion as a real card: done for Claude (#726), never run against a real claude.
- Real image blocks, effort option, Shift+Tab mode cycle, subagent text, persistent allow rules: open. The compaction notice: done (#724).
- Rewind, edit-and-resend and fork (T215): conversation rewind and fork done for Claude and Codex (#730); file rewind not built; Esc Esc, `/undo` and `/redo` not built.
- Settings surfaces (T217), MCP status, `/add-dir`, logout: open or decided against.
- Vim mode, themes, suggestions, paste chips, statusline: not needed.

Things not verified that change the verdict if wrong: what a real usage-limit result looks like; which todo tool claude uses in `-p` mode; whether hooks and `settings.json` permission rules apply under the adapter; whether the drawer places the permission card next to its tool row; whether structured transcripts survive an app quit; and whether the AskUserQuestion answer format and the rewind path work against a real claude.
