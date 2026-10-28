# ACP parity matrix: TUI actions against the structured pane

Task T197. Flockdeck's structured pane as of origin/main 4efb7311, against what a user can do in the TUIs of Claude Code, Codex, Gemini CLI and opencode.

## How to read this

Status is for the structured pane today:

- present: the user can do it and it works the same way.
- partial: something exists but a part is missing; the cell says which part.
- missing: no way to do it.

"Provided by" says where the fix comes from:

- ACP: a method, notification or capability in the protocol. The agent or adapter must implement it, so each row also depends on the agent. Whether a given agent does is not checked here, except where noted.
- client: Flockdeck builds it itself, with no protocol support needed.
- adapter: only possible if the agent's adapter does the work. Where Flockdeck writes the adapter (Claude, Codex) that is Flockdeck's code too.
- no: not possible over ACP or not worth building.

Sources and their limits:

- Claude Code: the commands page and interactive-mode page on code.claude.com, read 27 Oct 2026.
- opencode: opencode.ai/docs/tui, read 27 Oct 2026. It does not mention image paste or sharing.
- Gemini CLI: docs/reference/commands.md in the repo, read 27 Oct 2026.
- Codex: only the app composer's slash command page was readable (learn.chatgpt.com). The CLI's own slash command page redirected and I did not get it. Codex CLI rows marked (memory) are from what I know of the CLI and are not checked against its docs.
- ACP: agentclientprotocol.com protocol pages, read 27 Oct 2026. The pages do not label methods stable or unstable. Check the schema file for the pinned version before building on any row marked "check stability".
- Flockdeck: `internal/acp`, `internal/structured`, `internal/acp/bridge`, `ui/src/drawer/Structured.js` and `docs/structured-pane.md` on origin/main. I read code and did not run it.

I ran no paid sessions.

## What the structured pane does today

The client (`internal/acp`) implements initialize, authenticate, session/new, session/prompt, session/cancel and the permission request. It sends text-only prompts. It advertises no fs, no terminal and no elicitation capability. The bridge maps six update kinds: agent message, thought, tool call, tool call update, plan and usage. Everything else is dropped, including `available_commands_update`, `current_mode_update`, `config_option_update`, `session_info_update` and `user_message_chunk` (opencode's e2e notes show it sending available commands). The agent's capabilities from initialize are stored raw and not read.

The drawer (`Structured.js`) has a context and cost line, the conversation, a textarea with Send and Stop buttons, and the permission card. The queue (16 deep) is in the runtime; since API 1.19 the push lists it and the drawer draws it (T213). The plan is in the push and the drawer does not draw it. There is no key binding on the textarea.

## 1. Sending input

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| Send a prompt | all | present | `session/prompt` |
| Newline in the prompt | all | present | textarea |
| Keyboard send (Enter or Ctrl+Enter) | all | missing: button only | client |
| Interrupt the running turn | Claude Esc or Ctrl+C; Codex Esc/Ctrl+C (memory); Gemini Esc; opencode Esc | partial (T213): Stop button works (`session/cancel`) and drops the queue; "Stop, keep queue" ends the turn and goes on to the next prompt; the drawer says "Stopping the turn" until the turn ends. Esc, and Ctrl+C with no text selected, stop the turn and keep the queue (T227); double Esc is rewind, not built | client for the binding; `session/cancel` exists |
| Type while the agent works, input is queued | Claude, Codex (memory), Gemini, opencode | present (T213): up to 16 queued; the drawer says what is typed now waits, and lists the queue | client |
| See, edit or remove a queued prompt; send one now | Claude (Ctrl+Enter, take back what you queued) | present (T213, API 1.19): the queue is a list under the conversation with place, text and mail marked with its sender; Edit, Remove, Clear (the person's own) and Send now by item id; mail cannot be edited or removed. Reorder is not offered: Send now moves one to the front. Mail past the runtime's 16 waits in the store and is not listed | client |
| Mid-turn steering (queued text reaches the agent at the next tool boundary) | Claude, Codex | missing, and ACP has no method for it: one prompt is in flight at a time | no for true steering. Cancel then re-prompt is built (T213): Send now on a queued prompt cancels the turn and sends that prompt next. Adapter-internal injection in a Flockdeck-written adapter is not |
| Prompt history (Up, Ctrl+R) | all | partial (T212): Up and Down step through the last 50 prompts sent to the pane, kept per pane in the window's storage; no Ctrl+R search | client |
| Stash or restore a draft (Claude Ctrl+S), undo input edits | Claude | partial (T212): the draft is kept per pane across tab switches; no stash stack, and undo is the browser's own | client |
| Edit the prompt in `$EDITOR` (Ctrl+G, `/editor`) | Claude, opencode | missing, and a browser textarea makes it low value | client, low priority |
| Vim input mode | Claude, Gemini | missing | no (low value) |
| `@file` mention with autocomplete | all four | partial (T212): `@` opens a picker over the pane's files (`structuredFiles`, API 1.18) and inserts `@path` as text; the file is not attached as a `resource_link` or embedded | client for the picker over the pane's directory; ACP `resource_link` content block (baseline in the spec) or inline text; `embeddedContext` capability if the file contents are sent |
| `@dir` mention, opencode `@alias` reference roots | Gemini, opencode | missing | client, same route |
| `@session` message to another Claude session | Claude | missing | no: Flockdeck's own mail replaces it (T182) |
| `!` shell escape: run a command, output joins the conversation | Claude, Gemini, opencode | present (T214, API 1.20): `!cmd` in the prompt box runs in the pane's directory in a Flockdeck-owned shell as the person, with an allowlisted environment and a ten-minute limit; output streams into a list under the conversation as plain text, with Stop, Remove and Send to agent. A command is never sent to the agent unless the person presses Send to agent. Ended when the pane closes. One line only, desk only | client, `internal/shellrun`. ACP `terminal/*` is for the agent's use, not this |
| Paste or attach an image | Claude (Ctrl+V chips), Codex (`-i`, paste), Gemini (@ path), opencode (not documented) | partial (T212): paste, drop or Attach image saves the picture (`attachImage`) and puts `[image: path]` in the text for the agent to read, when `capabilities.image` is set; `Client.Prompt` still sends text blocks only, so no ACP image block yet | ACP image content block, gated by `promptCapabilities.image`; UI paste handler is client. The conversation already has an image row type |
| Attach other media (audio, PDF) | Gemini via `@` | missing | ACP `promptCapabilities.audio` / `embeddedContext` |
| Voice dictation | Claude `/voice` | missing | no (browser speech input is possible, low value) |
| Prompt suggestions (ghost text for the next prompt) | Claude | missing | adapter; no ACP method. Low value |

## 2. Slash commands

In ACP the agent advertises its commands with `available_commands_update` (name, description, input hint). The client shows them and runs one by sending `/name args` as ordinary prompt text. Flockdeck ignores the update, so none of this works. A slash command menu gets every command the agent decides to expose, including custom commands and skills.

Commands that do work only inside the TUI process (themes, key bindings, terminal setup) have no protocol equivalent and are out of scope. Commands that need agent-internal state need the agent to advertise them; if a Flockdeck-written adapter does not advertise `/compact` or `/cost`, the client must build them (rows below).

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| `/` menu with filter and argument hints | all | present in the structured drawer (T210); Flockdeck's own group has only `/stop` until mode, model and session commands exist | ACP `available_commands_update` plus client UI |
| Run an agent-advertised command (custom commands, skills, `/init`, `/review`, `/security-review`, `/simplify`, `/loop`, `/goal`, `/batch`) | all | missing | ACP: prompt text. Whether built-ins are advertised depends on the adapter. Claude's built-ins live in the CLI, not the SDK, so a Flockdeck Claude adapter must implement each one it wants |
| `/help` | all | partial: Flockdeck's own help, not the agent's commands | client |
| `/theme`, `/color`, `/statusline`, `/tui`, `/keybindings`, `/scroll-speed`, `/terminal-setup`, `/vim`, `/editor`, `/themes`, `/stickers`, `/radio`, `/mobile`, `/passes`, `/powerup`, `/release-notes`, `/docs`, `/privacy`, `/upgrade`, `/about`, `/bug`, `/feedback` | various | n/a: about the TUI or the vendor, not the work | no |
| `/ide`, `/chrome`, `/desktop`, `/remote-control`, `/teleport`, `/remote-env`, `/web-setup`, `/install-github-app`, `/install-slack-app`, `/setup-github`, `/schedule`, `/autofix-pr`, `/ultrareview`, `/background`, `/stop` | Claude, Gemini | n/a: vendor surfaces that Flockdeck's panes, relay and worktrees replace | no |

## 3. Models, modes and settings

ACP carries these as `modes` and `configOptions` in the `session/new` response, `session/set_mode`, `session/set_config_option`, `current_mode_update` and `config_option_update`. Models are normally a config option (a select with the category "model"); a separate `session/set_model` existed as an unstable method (check stability). Flockdeck now reads the response and the updates (structured pane push) and sends `session/set_mode`, `session/set_model` and `session/set_config_option` (T211, API 1.17); the rows below say what each action has today.

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| See the current model | all | present: the selector in the pane header shows what the agent reports (`models`, or a `configOptions` entry) | ACP: `models` or `configOptions` in the session response |
| Switch model | Claude `/model`, Alt+P; Codex `/model`; Gemini `/model`; opencode `/models`, Ctrl+X M | present: selector, `/model`, `/model <name>`; keys (Alt+P) are not bound | ACP `session/set_model` or `session/set_config_option` |
| Effort or reasoning level, thinking on/off, fast mode | Claude `/effort`, Alt+T, Alt+O; Codex `/reasoning`, `/fast`; opencode Ctrl+T | present for any select or boolean config option the agent offers, as a selector in the header; no `/effort` command and no keys. The own Claude and Codex adapters list no effort option, so none shows for them | ACP config options, if the agent offers them (category "thought_level" or similar). Otherwise adapter |
| Switch permission mode: default, accept edits, plan, auto, bypass | Claude Shift+Tab; Codex `/permissions` (memory); Gemini approval modes, `/plan`; opencode agents build and plan (Tab) | partial: selector, `/mode`, `/mode <name>` and `current_mode_update` are done; the Shift+Tab cycle is not bound | ACP `session/set_mode` plus `current_mode_update`. The agent can change its own mode, so the updates must be shown too |
| Enter plan mode / see the plan | Claude `/plan`; Codex `/plan`; Gemini `/plan`, `/plan copy`; opencode plan agent | partial: plan rows reach the push and the drawer draws them as a checklist (T228); the user cannot enter plan mode | ACP mode switch for entering; plan update for display (already received) |
| Approve or reject a plan (exit plan mode) | Claude, Gemini | partial: the Claude adapter sends the plan with the request and the card shows it as text (T226); not checked for Gemini; no "approve and accept edits" option | ACP permission request |
| Permission prompt: allow once, allow always, reject | all | present, desk only | `session/request_permission` |
| Add, edit or remove permission rules (`/permissions`) | Claude, Gemini `/permissions`, `/policies` | partial (T217): the only rules Flockdeck's layer keeps are the allow-always grants of the two own adapters, held in the adapter process for one session and offered for shell commands only (`claudeacp/perm.go` `allowed`, `codexacp/perm.go`). They are not listed anywhere and a restart of the pane drops them all. List and single revoke are not built; see section 11. Editing the agent's own rule files is the agent's (`/permissions` typed to it, if it lists it) | client for a view, but the rules are in the adapter, so a list needs an extension method on both adapters. No ACP method |
| Answer an agent's question (Claude AskUserQuestion, structured choices) | Claude, Codex | Claude: a card with the questions, options, multi-select and free text; the answers go back in the control response (T235, API 1.22). Codex: no counterpart. ACP `elicitation/create` is not advertised | ACP `elicitation/create` with client capability `elicitation`; check stability |
| Output style, language, agent persona | Claude `/output-style` | missing | ACP config option if offered |
| Additional directories for the session | Claude `/add-dir`, Gemini `/directory` | missing | ACP `additionalDirectories` on session/new (a capability); adding later needs a new session. `/cd` is not wanted: a pane is a directory |
| Sandbox toggle, trust folder | Claude `/sandbox`, Gemini `/permissions trust` | missing | adapter / client-side policy |
| Edit settings (`/config`, `/settings`, `/memory`, `/hooks`, `/skills`, `/plugin`, `/extensions`, `/agents`) | all | decided in section 11 (T217): none is built as a Flockdeck editor. What an ACP agent exposes as modes, models and options is in the header; the rest is the agent's own, reached by typing the command if the agent lists it | the agent. No ACP method |
| MCP servers: list, status, enable, reconnect | Claude `/mcp`, Gemini `/mcp`, Codex `/mcp` | partial (T217): Flockdeck passes its own server and the ones in `prefs.json` `mcpServers` to agents that take them, and Agent info lists the names it passed. Status, enable and reconnect: not possible, ACP has no method for them | ACP `mcpServers` on session/new. Status and enable/disable: no ACP method, adapter or notice |

## 4. Conversation lifecycle

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| Compact the context (`/compact`, `/compress`) with optional focus text | Claude, Codex, Gemini, opencode | missing | agent-advertised command (prompt text), or the ACP compaction update if the client declares `session.compaction`; check stability |
| Auto-compaction and its threshold (`/autocompact`) | Claude | partial: it happens agent-side, the context meter shows the drop, no notice | ACP compaction update or notice; threshold is a config option if offered |
| Clear and start a new conversation (`/clear`, `/new`) | all | missing; restart is the nearest and it starts a fresh process | ACP: a second `session/new` on the same process, then `session/close` for the old one |
| Resume a past session; session picker (`/resume`, `--continue`, `/sessions`, `/chat resume`) | all | missing; `docs/structured-pane.md` says a restart loses the agent's memory | ACP `session/list`, `session/load` (replays history as updates) or `session/resume` (no replay), gated by `loadSession` and `sessionCapabilities`. Flockdeck must store the session id per pane |
| Keep memory across a Flockdeck restart | all | missing | same as above |
| Fork or branch the conversation (`/branch`, `/fork`) | Claude, Codex | missing | ACP fork method if present (check stability); otherwise load plus truncate in a Flockdeck adapter |
| Rewind or undo to an earlier point, conversation and files (`/rewind`, Esc Esc, `/undo`, `/redo`, `/restore`) | Claude, Gemini, opencode, Codex (memory) | missing | no ACP method. Files: client, using git in the worktree (opencode does this too). Conversation: only a Flockdeck-written adapter can truncate history; a third-party adapter cannot |
| Edit an earlier user message and resend | Claude, Codex | missing | same as rewind |
| Rename the session | Claude, Codex, opencode | partial: Flockdeck has pane rename, not linked to the agent's session title | ACP `session_info_update` (title from the agent); rename itself is client |
| Side question that does not enter the history (`/btw`, `/side`) | Claude, Codex | missing | no ACP method. A second session with a copy of the context is possible in a Flockdeck adapter; low priority |
| Background tasks and shells (Ctrl+B, `/tasks`, `/shells`) | Claude, Gemini | missing for the agent's own background commands; the person's `!` commands run, list and stop (T214) | Decision, T214: Flockdeck does not advertise the ACP `terminal` capability, so an agent never calls `terminal/create` and there is nothing in `tool_call_update` `terminal` content to list. Advertising it would make Flockdeck run the agent's commands, which moves the permission gate from the agent to Flockdeck and needs its own task (process table, output limits, release, kill on close, a permission decision per command). Until then a command the agent backgrounds stays inside the agent (Claude's `run_in_background` Bash runs in the Claude process, which the Go adapter does not expose; I did not check Codex) and shows only as tool output. The adapters could report such tasks as a Flockdeck extension; not built |
| Subagent activity | Claude, Gemini, opencode | partial: tool rows show, nesting depends on the agent's tool calls; not checked | ACP tool calls; agent-side |
| Session recap (`/recap`) | Claude | missing | agent-advertised command |

## 5. Showing what the agent does

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| Streaming reply text, markdown | all | present | `agent_message_chunk` |
| Thinking blocks; toggle visibility (`/thinking`, Ctrl+O) | Claude, opencode | partial: thinking rows show, no toggle | `agent_thought_chunk` (received); toggle is client |
| Tool calls with output and diffs; expand | all | present | `tool_call`, `tool_call_update` |
| Live output of a long shell command | all | partial: only what the agent puts in tool content. The person's own `!` command streams its output (T214) | ACP `terminal` content, needs the `terminal` capability (not advertised, see background tasks) |
| File edits as diffs, `/diff` | Claude, Gemini | present: tool diffs plus the Changes tab, which goes beyond the TUI | n/a |
| Task checklist (Ctrl+T) | Claude | present (T228): drawn as a collapsible checklist; no Ctrl+T toggle, click the head | client |
| Context usage and its breakdown (`/context`) | Claude, Codex `/status` | partial (T216): header meter (bar and share) with a detail of used, window, left and cost; no breakdown, because `usage_update` has none | `usage_update` for the totals; breakdown: adapter only |
| Cost and token stats (`/cost`, `/usage`, `/stats`) | Claude, Gemini | partial (T216): cost shows in the header and the detail when the agent reports it, and says "Not reported" when it does not; no token split, no per-model or per-tool stats, because `usage_update` carries none | `usage_update` cost; per-turn usage in the prompt response (check stability); stats screens are client |
| Plan limits and rate limits (`/usage`, `/status`, `/rate-limit-options`) | Claude, Codex | missing | no ACP field. A Flockdeck adapter can send a notice or an extension notification |
| Wait for a limit reset and continue | Claude | missing | client: schedule a re-prompt; low priority |

## 6. Copy, export, search and history

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| Copy the last response (`/copy`, `/copy N`) | Claude, Gemini | present (T216): toolbar Copy for the loaded entries, Copy last reply, a Copy button on each reply, and Copy on each code block | client |
| Export the conversation (`/export`, `/chat share`) | Claude, Gemini, opencode | present (T216): Export in the conversation bar writes a Markdown file into a folder the person names (`structuredExport`, API 1.20), redacted, with mail marked and thinking only if chosen. Desk only. No JSON form | client: serialise the bridge entries to Markdown or JSON; or ACP `session/load` replay if the agent's own history is wanted |
| Save and restore a named checkpoint | Gemini `/chat save` | missing | client, with session load |
| Search the conversation | Claude (Ctrl+R is history, transcript search), others | present: `conversationSearch` | n/a |
| Scroll back through long history | all | present: pages, Older | n/a |
| Transcript viewer | Claude Ctrl+O | present in the form of the conversation view | n/a |

## 7. Account, notifications and safety

| Action | In which TUI | Status | Provided by |
| --- | --- | --- | --- |
| Log in | all | partial (T217, T232): a pane in `needs setup` shows what the agent asked for in the Agent info panel, which opens by itself. Since API 1.24 (T232) the panel has a Sign in button when the adapter lists the `flockdeck-login` method (Claude and Codex do): the adapter runs `claude auth login` or `codex login`, the panel shows the address it prints as a link, and the pane restarts when the command ends well. No terminal is needed, Flockdeck asks for no password or key and stores nothing; the address is the only agent-originated string and is checked three times. Other adapters keep the manual route: a button that restarts the agent once the person has signed in with the agent's own tool. The catalog advice for `claude-acp` is still "Run `claude` once in a terminal and sign in with your Claude subscription, or store an Anthropic API key in Settings." Checked against claude 2.1.311 on 27 Oct 2026: with no login the prompt is refused as authentication required; with a login a one-word prompt is answered | `claude auth login` was checked live when piped (address on stdout); `codex login` was not (`docs/acp-claude-adapter.md`, "Signing in from Flockdeck") |
| Use an API key instead of a login | Claude `ANTHROPIC_API_KEY`, Gemini `GEMINI_API_KEY`, Codex `OPENAI_API_KEY` | present for the hidden structured entries `gemini-acp`, `codex-acp` and `claude-acp`: a key stored in Settings or with `flockdeck keys set` is set in that agent's environment only, under its one variable. Not checked: a Claude pane started with a stored key against the real API | client |
| Log out, change auth method | Claude `/logout`, Gemini `/auth`, opencode `/connect` | not built (T217): the panel says to use the agent's own tool and restart. The client has no `logout` call, and for Claude and Codex the login is a file in the agent's own config that Flockdeck must not delete or rewrite | ACP `logout` (auth capability); `authenticate` with another method |
| Desktop alert when the agent needs a permission or finishes | Claude hooks, terminal bell | partial (T234): a permission wait, a setup need and a finished turn raise a desktop notification while the window is behind; a phone gets no push, only the waiting status in its list | client |
| Phone push for a permission or a finished turn | not a TUI feature, but Flockdeck terminal panes have it | missing by design: structured data is refused over the relay | client plus sealed transport (T182 decision 5) |
| Stop all subagents (Ctrl+X Ctrl+K) | Claude | missing | adapter |
| Quit or exit | all | present: close the pane | n/a |

## 8. Ten biggest gaps

By how often a user hits them and how little of it exists:

1. Slash command menu from `available_commands_update` (not read at all).
2. Resume, history and session list (`session/load`, `session/resume`, `session/list`); today a restart wipes agent memory.
3. Model, effort and mode switching (`modes`, `configOptions`, `session/set_mode`, `session/set_config_option`), including plan mode and the permission-mode cycle.
4. `@file` mentions with a picker (client) and `resource_link` blocks.
5. Image paste and attachment (content blocks, `promptCapabilities.image`).
6. Visible, editable queue (done, T213, T199); key bindings for interrupt are still missing.
7. Rewind, undo, edit-previous-message and fork. No ACP method; needs git snapshots plus a Flockdeck-written adapter that can truncate history.
8. Clear and new conversation, plus compaction commands and notices.
9. `!` shell escape (done, T214) and background tasks (`terminal/*`: the capability is not advertised; the agent's own background commands are not listed).
10. Export, per-message copy and usage detail (done, T216); plan drawing and task checklist are still open.

Not in the ten but large: config and settings editing (skills, plugins, hooks, permission rules, MCP status) and the Claude and Codex adapters themselves, since most Claude and Codex commands live in the CLI, not the SDK, and a Flockdeck adapter has to re-implement the ones the user needs.

## 9. Open questions

- Which of the ACP methods above the pinned opencode, Gemini and the two Flockdeck adapters implement. `agentCapabilities` is already returned by initialize and stored, so a one-hour spike can print it per agent.
- Whether Claude's headless SDK modes can set permission mode, model and interrupt mid-session (I believe the control protocol allows it, not verified), which decides how much of section 3 the Claude adapter can offer.
- Codex's CLI slash command list, which I could not read.
- Which unstable ACP methods Flockdeck is willing to depend on.

## 10. Gemini CLI and Codex checked (T192, 27 Oct 2026)

Neither agent is installed on the machine that did this, so every "does agent X do it" cell above is still
unchecked for Codex and checked against Gemini's source only. From that source: Gemini returns
`loadSession: true` and no `session/resume`; its permission requests offer allow_once, allow_always and
reject_once; it takes MCP servers over stdio, http and sse; it needs `authenticate` for a key (fixed, see
[adapters](acp-adapters.md#gemini-cli-and-codex-end-to-end-t192)). `internal/acp/live_agents_test.go` runs the
whole sequence (start, session, prompt, permission card, cancel, resume) against a real agent when one is
installed. Neither agent was unhidden.

## 11. Config surfaces (T217, 27 Oct 2026)

Each surface is one of: (a) Flockdeck already covers it, (b) the person types the slash command to the agent, so nothing to build, (c) Flockdeck builds a surface. What the agent lists in `available_commands_update` is the agent's choice, and I ran no paid session, so "if listed" below is not checked per agent. Claude's built-in commands live in the CLI and the own Claude adapter lists what claude's headless `initialize` returns; the own Codex adapter lists `review`, `compact` and the enabled skills only (`codexacp/session.go` `fixedCommands`). So for these two a built-in such as `/memory` may not be there to type.

| Surface | Decision | Evidence and what is there |
| --- | --- | --- |
| `/config`, `/settings` | (a) for what ACP exposes, otherwise (b) | Mode, model and every select or boolean option the agent offers are selectors in the pane header (T211). Flockdeck's own settings are the app's. Anything else is the agent's own and typed if listed |
| `/permissions` | (a) for the card, (c) for the rules, deferred | The permission card (allow once, allow always, reject) is done and desk only. The allow-always grants live in the adapter process, per session, and only for shell commands. Listing or revoking one needs a new extension method on the Claude and Codex adapters, the client and the runtime, plus a view; that is its own task. Until then, a pane restart drops every grant, and the Agent info panel does not claim a list |
| `/mcp` | (c) names, nothing more | Agent info lists the names of the servers the session was given. Command, arguments, headers and environment values are not read into the view. Status, enable and reconnect have no ACP method |
| `/hooks`, `/memory` (CLAUDE.md, AGENTS.md), `/agents`, `/skills`, `/plugin`, `/extensions` | (b) | They configure the agent's own files and process. Flockdeck editing those files would be a second editor for state the agent reads; skills the agent lists arrive as commands in the `/` menu (T210). Not built |
| `/init` | (b) | An ordinary command if the agent lists it; Codex has no such command listed by the own adapter |
| `/login` | (c) small | Agent info opens by itself when the pane waits for a login, shows the agent's request and the entry's `login` advice, and, for an adapter with the `flockdeck-login` method, a Sign in button that sends `structuredSignIn` (API 1.24) and shows the address as a link; for any other adapter a button that restarts the agent after the person signed in with the agent's own tool (`restartPane`). No credential field either way |
| `/logout` | not built | No `logout` call in the client; the login is the agent's own file. The panel says to use the agent's tool and restart |
| `/status`, `/doctor` | (c) status, (b) doctor | Agent info (API 1.23, `structuredInfo`, also `/info`): agent name and version from initialize, state, catalog entry, adapter, directory, session id, sign-in method names, mode, model and options, MCP server names, capabilities, slash command count. Plain text, bounded, desk only, refused to relay windows and viewers. Plan limits are not in it because ACP has none (section 5). `/doctor` checks the agent's install and is typed if listed; the launch permission check (`PermissionNote`) is not shown in the panel |
| `/help` | (a) | Flockdeck's own help page. The agent's own `/help` is typed if listed |
| `/theme`, `/terminal-setup` | not applicable | About the agent's TUI. There is no TUI in a structured pane |

Not run: no real Claude, Codex or Gemini session, so what each lists is unchecked; the slow `internal/workspace` tests, because nothing in that package changed.
