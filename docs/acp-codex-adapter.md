# Codex adapter

Flockdeck has its own ACP adapter for Codex. It is the `acp-adapter codex`
subcommand of the `flockdeck` binary: `flockdeck acp-adapter codex` speaks ACP on
stdin and stdout and starts one `codex app-server --listen stdio://` process per
session. It needs nothing except the `codex` executable. The manifest entry
(`codex-go` in [`adapters.json`](../internal/acp/manifest/adapters.json)) is
hidden, so it is not in the table on [Structured-agent adapters](acp-adapters.md)
and the canary does not try to install it. The `codex` entry there is still the
third-party npm adapter.

The code is in `internal/acp/codexacp`. The route is researched in
[Codex adapter route](codex-adapter-route.md).

## What has and has not been run

No real Codex has run against this adapter. The machine it was built on has no
`codex`, so there was no schema from `codex app-server generate-json-schema`
either. A check of the approval policy and sandbox on 27 Oct 2026 (task T220) also
found no `codex` on the machine (not on `PATH`, not in the npm directory), so it was a
review of the code against the app-server protocol as documented, plus tests on the
fake. Under "Policy review" below, each point says what was checked and what is still
open. The wire shapes come from the route document and from what I remember of
the app-server's v2 protocol, and the tests run against a fake app-server that
replays those shapes. A real session is the first thing to do before this
adapter is offered. The parts most likely to be wrong are listed under
"Unverified" below.

## How it drives codex

| ACP | codex app-server |
| --- | --- |
| `initialize` | answered by the adapter; no codex process yet |
| `session/new` | starts codex, `initialize` then `initialized`, `thread/start`, `model/list`, `skills/list`; the session id is the thread id |
| `session/resume` | the same with `thread/resume` for the given thread id |
| `session/prompt` | `turn/start`; ends at `turn/completed` for that turn's id |
| `agent_message_chunk` | `item/agentMessage/delta`, or the whole text of a completed `agentMessage` that had no deltas |
| `agent_thought_chunk` | `item/reasoning/summaryTextDelta`; the raw `textDelta` only when the item has no summary |
| `tool_call`, `tool_call_update` | `item/started`, `item/commandExecution/outputDelta`, `item/fileChange/patchUpdated`, `item/completed` |
| `plan` | `turn/plan/updated` |
| `session/request_permission` | `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval` |
| `session/cancel` | `turn/interrupt` |
| `session/set_mode` | the approval policy and sandbox sent with the next `turn/start` |
| `session/set_model` | `model` on the next `turn/start` |
| `usage_update` | `thread/tokenUsage/updated`: used is the last request's total, size is `modelContextWindow`. No cost: codex reports none |
| `available_commands_update` | `skills/list`, plus `/review` and `/compact` |

The session id is the thread id, so a client that stored it can pass it to
`session/resume` later. The adapter advertises `loadSession: false` and
`sessionCapabilities.resume`: it resumes the thread but does not replay its
history.

Messages to codex leave out the `"jsonrpc"` member, as codex's own do.

### Tool calls

A command's card shows its first line as the title; the whole command is in
`rawInput`. A command is `execute`, unless codex's command actions are all reads (`read`) or
all listings and searches (`search`). A file change is `edit`. For an added file
the diff is the file's content, for a deleted file the old content, and for an
update codex's unified diff is shown as text. MCP tools, dynamic tools, web search
and sub-agent activity are shown with a title. A command's output is sent as it
arrives, at most every 200 ms, and the full output follows with the completed
item. Output and diffs are cut at 64 KiB and 256 KiB. A file change of more than 50 files, or with a
file whose diff was cut, shows "N more files are not shown" and is declined, not
asked about: the card could not show what the person would be allowing. Items are matched by their
id, never by position, so results that finish out of order land on the right
call.

### Modes and models

Three modes, each an approval policy and a sandbox:

| Mode | Approval policy | Sandbox |
| --- | --- | --- |
| `auto` (default) | `on-request` | `workspace-write`, no network |
| `read-only` | `on-request` | `read-only` |
| `ask` | `untrusted` | `workspace-write`, no network |

The mode with no sandbox and no questions is left out on purpose, as
`bypassPermissions` is in the Claude adapter. A mode change applies from the next
turn. `/review` sends no policy of its own and runs under the one the last turn
carried, so a `/review` after a mode change is refused with an error until a normal
turn has been sent. The check and the write of `review/start` are one step with respect
to a mode change, so a change cannot land in between. `/compact` runs no tools and is
not held back.

The models come from `model/list`. Reasoning effort is not offered yet.

### Slash commands

A prompt that is only `/compact` runs `thread/compact/start`. A prompt that is
`/review`, with or without instructions, runs `review/start` on the uncommitted
changes or with the instructions as a custom target. A prompt that starts with the
name of a skill codex listed is sent with a skill item for it, followed by the rest
as text. Anything else that starts with a slash is sent to codex as text.

### Permissions

Each card offers Allow and Reject. A shell command also offers "Always allow this
command". The adapter keeps that rule for the life of the session, in memory only,
and never sends codex `acceptForSession` or an execpolicy or network amendment:
what those cover is up to codex. The rule is the command text and its directory.
It is not offered when the request has a network context, or any member the
adapter does not know about. A file change and an extra-permissions request are
asked about each time.

A card appears only while a prompt of ours is running: a request that arrives with
no turn, after a `session/cancel`, or after the turn's generation changed is
refused with `cancel` and never shown. An extra-permissions request longer than the
2,000 bytes the card shows is refused, not granted.

Everything fails closed. A refusal is `decline`. A request that names another
thread, has no item id, arrives when 32 are already waiting, or is of a kind the
adapter does not know gets a refusal or a JSON-RPC error. An answer of "cancelled",
a client error, an answer that cannot be read or an option that was not offered is
a refusal (`cancel` for a cancelled card). `allow_always` for a card that did not
offer it is a refusal. For extra permissions, allow grants exactly what codex asked
for, for the turn; a refusal grants nothing.

On `session/cancel` every waiting card is refused with `cancel` and withdrawn, then
the turn is interrupted. An answer from the client that arrives afterwards is
dropped. If codex settles a request itself (`serverRequest/resolved`) the card is
withdrawn without an answer. "Withdrawn" is a statement by the adapter: the card
stays on the client until the person closes it, and an Allow given afterwards is
dropped. Questions the model asks (`item/tool/requestUserInput`),
MCP elicitations and the legacy `applyPatchApproval` and `execCommandApproval`
forms get an error.

### Prompts during a turn

A `session/prompt` that arrives while a turn is running is queued. Each prompt gets
its own turn and its own stop reason, in order, and nothing is sent to codex until
the turn before it has ended. `session/cancel` interrupts the running turn and
answers every queued prompt `cancelled` without sending it. A cancel that arrives
before codex has told us the turn's id is held and sent when the id arrives.

Codex's `turn/steer` could fold a second message into the running turn. The adapter
does not use it, for the reason given in the [Claude adapter](acp-claude-adapter.md).

### Compact and review

These two answer before any turn is announced, and the prompt ends with the turn that
follows. If codex names no turn within 30 seconds of its answer, the adapter takes it
that nothing started and ends the prompt with `end_turn`.

### Cancel

If the turn has not ended 15 seconds after the interrupt, the adapter ends the
codex process and the prompt fails; later prompts in that session fail too.

### Images and MCP servers

An image block is decoded and checked: PNG, JPEG, GIF or WebP, 20 MiB, 16 per prompt,
200 MiB per session, and the first bytes must match the declared type. It is written to a file in a directory of its own (`flockdeck-codex-img-*`, private to
the user, files mode 0600). Codex gets a `localImage` item with the path. The
directory is removed when the session ends. The session touches it whenever it adds
an image, and one that has not been touched for a week, left by an adapter that was
killed, is removed at the next start. A resource link and an embedded text
resource become text. Audio is refused.

MCP servers in `session/new` go to codex as `config.mcp_servers` in the
`thread/start` request. Nothing is written to disk or put on a command line, where a
token would show. SSE servers are refused.

### Errors

An error sent to the client never carries text from codex. If `codex` cannot be
started, or fails during `session/new`, the error is "Codex is unavailable". A
failed turn is "the turn failed"; a turn codex says failed for a missing login
becomes error `-32000` "Authentication required"; a context window failure ends the
prompt with the stop reason `max_tokens`. A codex that exits mid-turn fails the
prompt with "the agent stopped during the turn". The detail goes to the adapter's
stderr only. The client's `ExitError` puts the tail of that stderr in its message
when the adapter itself exits; whoever shows that to a person should not show the
stderr part.

### Limits and exit

A line from codex longer than 16 MiB ends the session: codex is killed, the running
prompt fails and later prompts fail. A line from the client longer than 32 MiB stops
the adapter. Neither line is ever held in memory whole. Codex's stderr is read to the
end and logged. Codex is ended on every exit path: a failed start, a session that is
still starting when the adapter is told to stop, input that ends, an interrupt that
does not work, and SIGTERM, SIGHUP on Unix or an interrupt, which close every session
first. `Run` returns when its context is done whether or not its input is still open.

### Logs

The adapter's stderr holds codex's own error text and stderr lines, with the
environment and header values of the session's MCP servers replaced by
`[redacted]` (values under four characters are left, and a `Bearer <token>` value
is redacted as a whole and as the token). It is a log and nothing else. The client's
`ExitError` carries the tail of it, and that tail must never be shown to a person.

### Environment

The adapter starts codex in the environment it was given, minus every `FLOCKDECK_` and
`PERCH_` variable in any letter case, also when the caller passes an environment of its
own. The client must pass what codex reads its login and settings from (`HOME` or
`USERPROFILE`, `APPDATA`, `CODEX_HOME`, `OPENAI_API_KEY` if used); the client's default
environment has none of them. `FLOCKDECK_CODEX_BIN` names the executable when it is not
on `PATH`.

## Unverified

These were not checked against a real codex:

- That the app-server takes the messages as sent, without `"jsonrpc"`, and with
  `capabilities.experimentalApi` false.
- The field names and shapes of `thread/start` and `thread/resume` results, `model/list`,
  `skills/list`, `turn/start`, `review/start`, the item types, `turn/completed` errors
  (`codexErrorInfo` as a string or an object), `thread/tokenUsage/updated` and
  `turn/plan/updated`.
- `config: {"mcp_servers": {...}}` in `thread/start` as the way to hand over MCP servers,
  including `http_headers` for HTTP servers.
- The `sandboxPolicy` objects sent with `turn/start` (`workspaceWrite` with
  `writableRoots` and `networkAccess`, `readOnly`), and the `skill` and `localImage`
  input items.
- The response to `item/permissions/requestApproval`: `{permissions, scope: "turn"}`, with
  an empty `permissions` for a refusal.
- That `thread/compact/start` and `review/start` produce `turn/started` and
  `turn/completed` like a normal turn, which the adapter relies on to end the prompt.
- That `serverRequest/resolved` carries `requestId` in the same form as the request's id.
- Whether a codex that is not logged in fails the first turn with `unauthorized`, as the
  adapter assumes, or fails earlier.
- That the `thread/start` and `thread/resume` results report the approval policy and
  sandbox that took effect, as `approvalPolicy` (a string) and `sandbox` (a string, or an
  object with `type` and, for `workspaceWrite`, `networkAccess`). The adapter requires both:
  a result without them, with them null, or in a form it does not know is treated as
  unavailable, and so is one that is looser than the mode or has `networkAccess` set to
  anything but `false`. A codex that omits these members will therefore not open a session
  until this has been checked against a real one.
- Approvals from a sub-agent's thread would carry another thread id and are refused.

## Policy review

Read against the protocol, not run. Checked, and what it means:

- `thread/start`, `thread/resume` and `turn/start` all carry the mode's approval policy and
  sandbox, so a user's `config.toml` does not set them. `session/new` and `session/resume`
  fail with "Codex is unavailable" if the answer reports something looser or reports
  nothing (the reason goes to the log only). Only the approval policy, the kind of sandbox
  and its network access are compared. Writable roots, read access, `excludeSlashTmp` and
  `approvalsReviewer` are not: every `turn/start` sends the explicit policy, which replaces
  what the thread started with, so a difference there lasts until the first turn and
  cannot widen anything the mode names.
- In `auto` and `read-only` the policy is `on-request`: codex decides when to ask, and the
  sandbox is what stops a command. A command the model asks to run outside the sandbox
  arrives as `item/commandExecution/requestApproval` and gets a card. In `ask`
  (`untrusted`) codex asks for anything it does not consider known-safe, which includes
  commands it reads as safe reads; those run without a card.
- What runs without asking in `auto`: commands inside the sandbox, which can write the
  working directory and codex's usual temporary directories, with no network. `.git`
  handling and the temp directories are codex's choice and were not seen.
- Every approval method the adapter knows is answered `accept` only for an option it
  offered; everything else is `decline` or `cancel`, or a JSON-RPC error for methods it
  does not know. The legacy v1 approval requests get an error, which codex reads as a
  denial.
- `review/start` and `thread/compact/start` carry no policy. The adapter now refuses
  `/review` while the mode differs from the one the last turn was sent with.
- Cancel sends `turn/interrupt` and, after 15 seconds, ends the codex process tree. Exit
  ends the process on every path (see "Limits and exit").

Still to run on a real codex: a command outside the sandbox raising a card, a rejected
card, the echo fields above, `turn/interrupt` mid-command, `thread/resume` in a new
adapter process, and a Windows run, where the sandbox may need setup first
(`windowsSandbox/setupStart`) and may be weaker than on other systems.

## Signing in from Flockdeck

The adapter lists the same one method as the Claude adapter (`flockdeck-login`) and answers
`authenticate` for it (T232, API 1.24). The route, the rules and the reasons are in
`docs/acp-claude-adapter.md`, "Signing in from Flockdeck"; only the differences are here.

- The command is `codex login`, run as a process of the adapter. The adapter does not use the app
  server's `account/login/start`: that would tie the sign-in to the app server's protocol and to a
  codex app-server process kept for it. `codex login` is one command with one exit code.
- Hosts the address may be on: `openai.com` (`auth.openai.com`) and `chatgpt.com`, and their
  subdomains.
- `codex login` starts a local callback server on a fixed port on the person's machine; the
  address it prints is on the host above, never the local one (a port is refused anyway).

Not verified: codex is not installed on the machine this was written on, so neither the wording of
its output nor that it prints the address when its output is not a terminal was seen. The tests use
a fake. Check with a real codex before relying on it; until then the pane's manual fallback ("sign
in with `codex` in a terminal, then restart") stays in the catalog advice.

## Not done

- `session/load` with history replay, and `session/list`. A restored Codex pane therefore opens with an empty transcript while codex still has the thread. The app-server has history calls (`thread/read`, `thread/turns/list`, `thread/items/list`; `docs/codex-adapter-route.md`), but the shape of their results has not been seen in a real codex, and none is installed where T230 was done (28 Oct 2026). Left until a real run shows the shapes. The Claude adapter's reader (`docs/acp-claude-adapter.md`) is the model: replay through `session/load`, no API change.
- Logging in through the app server's `account/login/start`, and API-key use other
  than through the environment.
- Reasoning effort, the mode with no sandbox, the cost of a turn, and `turn/steer`.
- Questions the model asks and MCP elicitations. They are refused, which tells the model
  nobody is there.
- Finding or downloading a pinned codex binary, and Windows sandbox setup
  (`windowsSandbox/setupStart`), which may need a first-run step.
- Turns codex starts without a prompt. Their updates are forwarded as `session/update`, but
  nothing waits for them.

## Tests

`go test ./internal/acp/codexacp` runs the adapter against a fake app-server, which is the
test binary started again. The fake answers the session requests itself, plays a script of
lines for the rest, and logs everything it receives, its arguments, its environment and its
process id. One test starts the adapter as a child and drives it with `internal/acp`'s
client. No test starts a real codex.
