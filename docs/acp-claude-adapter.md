# Claude Code adapter

Flockdeck has its own ACP adapter for Claude Code. It is the `acp-adapter claude`
subcommand of the `flockdeck` binary: `flockdeck acp-adapter claude` speaks ACP on
stdin and stdout and starts one `claude` process per session. It needs nothing
except the `claude` executable. The manifest entry (`claude` in
[`adapters.json`](../internal/acp/manifest/adapters.json)) is hidden, so it is not in the
table on [Structured-agent adapters](acp-adapters.md). The catalog entry that starts it
is `claude-acp`, also hidden (see [The catalog entry](#the-catalog-entry)). The canary
builds the `flockdeck` binary and runs the adapter against a real `claude` (see
[The canary](#the-canary)).

The code is in `internal/acp/claudeacp`.

## How it drives claude

Each session runs

```
claude -p --input-format stream-json --output-format stream-json --verbose
       --include-partial-messages --permission-prompt-tool stdio --session-id <uuid>
```

in the session's directory. `--permission-prompt-tool stdio` is not in the CLI
reference; the flag and the control messages below were found by experiment (see
the [spike report](claude-headless-spike.md)) and match what the Agent SDK sends.
They can change between claude versions, so the manifest records the version
the adapter was tested with (2.1.311); see
[Installed version and the pin](#installed-version-and-the-pin).

| ACP | claude |
| --- | --- |
| `initialize` | answered by the adapter; no claude process yet |
| `session/new` | starts claude, sends the `initialize` control request, returns the modes and models it lists, then sends `available_commands_update` from its commands |
| `session/prompt` | one `user` line: text blocks as text, image blocks as base64 image blocks, a text resource or link as text; ends at claude's `result` line |
| `agent_message_chunk`, `agent_thought_chunk` | `text_delta` and `thinking_delta` stream events |
| `tool_call`, `tool_call_update` | `tool_use` blocks and `tool_result` blocks |
| `plan` | the `TodoWrite` tool |
| `session/request_permission` | a `can_use_tool` control request |
| `session/cancel` | an `interrupt` control request |
| `session/set_mode` | `set_permission_mode` (default, acceptEdits, plan) |
| `session/set_model` | `set_model` |
| `usage_update` | the `result` line: context size from the last assistant message, window from `modelUsage`, cost from `total_cost_usd` |

Slash commands, skills and custom commands need no mapping: a prompt that starts
with `/review` is sent as text, and claude runs it. See
[/compact, /clear and /context](#compact-clear-and-context) for the built-ins.

### Tool calls

The kind is read from the tool name (Read is `read`; Edit, Write and NotebookEdit
are `edit`; Bash is `execute`; Glob and Grep are `search`; WebFetch and WebSearch
are `fetch`; Task is `think`; anything else is `other`). An edit carries a diff: Edit
and MultiEdit use the old and new strings of the input, Write reads the file as it
is on disk (up to 1 MiB) for the old text. A result longer than 64 KiB is cut. The
tool calls of a subagent are shown; its text is not.

### Permissions

Each card offers Allow and Reject. A shell command (Bash, PowerShell) also offers
"Always allow this command". The adapter keeps that rule for the life of the
session, in memory only. The rule is the tool and its whole input except the
description, so the same command with `dangerouslyDisableSandbox`,
`run_in_background` or a different timeout is a different call and is asked about.
The rule matches the command text, not what it does: it also covers whatever the
command runs, such as a script after the script has been edited, or a relative
path after a `cd` that went somewhere else.

No other tool gets an always option. A rule for Edit, Write, WebFetch, Agent or an
MCP tool would also cover calls nobody saw: a path outside the project,
`.claude/settings.json` or `.git/hooks`, any URL, any prompt. To stop being asked
about edits, use the acceptEdits mode. If a client sends `allow_always` for a card
that did not offer it, the adapter treats it as Allow.

A card answered "cancelled" is denied. A request that claude retracts, or that a
cancel overtakes, is dropped without an answer, also when the client's answer
arrived at the same moment. Any other control request from claude is answered with
an error so it does not wait.

The real shape of an `ExitPlanMode` request was captured from `claude` 2.1.312 in plan mode
(27 Oct 2026): `input` is `{"plan": "<the plan as markdown text>", "planFilePath": "<path of the plan file under ~/.claude/plans>"}`, and the request has
`"requires_user_interaction": true`. The plan text is in `plan`; the file path is not read. There was no `allowedPrompts` field in that capture.

`ExitPlanMode` goes to the client with the plan as text content of the tool call
(cut at 64 KiB here and at 32 KiB on the card). A request with no plan text is denied
with a fixed message and no card, so a plan is never approved unseen. The same
holds for an edit: the tool call of a permission request carries the diff.

`AskUserQuestion` is a card too. The request goes out as `session/request_permission` with the
tool call kind `question` and the input as `rawInput`, and two options: `answer` (allow once)
and `reject`. The client answers `answer` with `_meta.flockdeck.answers`, a list of
`{question, selected, other}` that name each question and option by position. The adapter
checks them against the input it kept (every question once, positions in range and not
repeated, one thing for a question without `multiSelect`, `other` at most 2000 bytes) and
allows the call with `updatedInput` set to the input plus `answers`, an object from the
text of each question to the labels chosen, joined by ", ", and any text typed. Claude reads
that object as the person's answer. The input is denied with a fixed message, and no card is
made, unless it has one to four questions with distinct non-empty texts, at most eight
options each with distinct non-empty labels that do not hold ", ", and text within the caps
(question and description 2000 bytes, label and header 200). The label given back to claude is the label as the card shows it (cleaned of control, format and bidi
characters and cut to one line), so what the person chose is what claude is told; a label that cleans to nothing, or
to the same as another's, or that holds ", ", is a refusal of the whole request. Skip is denied with "The user
declined to answer the questions.", and a cancelled card or answers that do not fit with "The
question was cancelled." The answers format was taken from Claude Code's documented
`AskUserQuestion` tool input and has not been run against a real `claude`; the fake claude
covers the adapter side.

### Prompts during a turn

A `session/prompt` that arrives while a turn is running is queued. Each prompt gets
its own turn and its own stop reason, in order, and nothing reaches claude until
the turn before it has ended. `session/cancel` interrupts the running turn and
answers every queued prompt `cancelled` without sending it.

claude can instead take a second message mid-turn and fold it into the running turn
(one `result` for both). The adapter does not do this, because two ACP prompts
would get one stop reason. It is the open question below.

### Cancel

The interrupt ends a running tool and retracts a pending permission request. The
claude process stays up and takes the next prompt. If the turn has not ended 15
seconds after the interrupt, the adapter ends the claude process and the
prompt fails; later prompts in that session fail too.

### Errors

An error sent to the client never carries text from claude. If `claude` cannot be
started or exits during `session/new`, the error is "Claude Code is unavailable".
A refused model or mode change is "the agent could not change the model" or "...the mode",
and a claude that exits mid-turn fails the prompt with "the agent stopped during the turn".
The detail goes to the adapter's stderr only. A result that says the login is missing
becomes error `-32000` "Authentication required", which the client turns into its
authentication state.

### Failed turns

A result with `is_error` true (or `error_during_execution`) is told to the client as
error `-32603` with a fixed message and a class in the error's `data`:
`{"class": "usage_limit", "resetsAt": 1793143200}`. The class is read from structured
fields only, never from claude's text. The first row that matches wins:

| Class | Message | Taken from |
| --- | --- | --- |
| `context_full` | The conversation is too long for the model | `terminal_reason` `prompt_too_long`, `blocking_limit` or `rapid_refill_breaker`; `api_error_status` 413 |
| authentication (error `-32000`) | Authentication required | the assistant line's `error` is `authentication_failed`, `account_on_hold`, `oauth_org_not_allowed` or `verification_required`; status 401 |
| `usage_limit` | Usage limit reached | `error` `rate_limit` or `billing_error`; status 429 |
| `outage` | The service is overloaded or unreachable | `error` `server_error`; status 408 or 500 and up (529 is overloaded) |
| `usage_limit` | Usage limit reached | a `rate_limit_event` line with status `rejected` in the same turn, when no row above matched |
| `refused` | The model declined this request | `stop_reason` `refusal` |
| authentication (error `-32000`) | Authentication required | old claude that sent no code: the text says `/login`, "not logged in" or "invalid api key" |
| `other` | the turn failed | everything else, a model that does not exist included (status 404) |

The `error` code is a member of the assistant line that carries claude's error
message. It comes before the result in the same turn, so the adapter keeps it per turn
and clears it when a turn starts and when it ends. A sub-agent's assistant line is
ignored. `resetsAt` is the reset time of a `rejected` `rate_limit_event` (Unix seconds)
and is sent for a usage limit only. A result that is not an error and has `stop_reason`
`refusal` is still the ACP stop reason `refusal`.

The client (`internal/structured/failure.go`) maps the class to its own text and
ignores the message. A class it does not know gives "the agent reported an error". A
usage limit adds ", resets at 15:30" (or "Wed 28 Oct 20:00" on another day, in the local
zone) only when `resetsAt` is a number between now and 31 days ahead; any other value
leaves the time out. The turn ends with the notice "Turn ended: failed: <text>", and
the session takes the next prompt.

Observed with claude 2.1.312 on 27 Oct 2026 (throwaway directory):

- A model that does not exist (`--model nosuchmodel-xyz`): an assistant line with
  `error: "model_not_found"` and `api_error_status: 404`, then a result with
  `is_error: true`, `subtype: "success"` (not an `error_*` subtype), `api_error_status: 404`,
  `terminal_reason: "api_error"` and the message in `result`.
- No network (`ANTHROPIC_BASE_URL` pointing at a closed port): ten `system/api_retry`
  lines (`error: "unknown"`, `error_status: null`, delays growing to 38 seconds), about
  four minutes in all, then an assistant line with `error: "server_error"` and a result
  with `terminal_reason: "api_error"`.
- A usage limit and a full context could not be provoked. Their shapes come from the
  `claude` binary's own strings: `rate_limit_event` with `rate_limit_info`
  (`status`, `resetsAt` in seconds, `rateLimitType`), the assistant `error` codes above,
  and the `terminal_reason` list (`blocking_limit`, `prompt_too_long`, `api_error`,
  `model_error`, and others). The tests use those shapes; a real limit has not been seen.
- Every turn also sends a `rate_limit_event` with status `allowed` while the account
  has room. Only `rejected` counts.

The client's `ExitError` puts the tail of the adapter's stderr into its message
when the adapter itself exits; whoever shows that to a person should not show the
stderr part.

A line from claude longer than 16 MiB ends the session: claude is killed, the
running prompt fails and later prompts fail. The line is never held whole in
memory. The adapter stops on an interrupt, and on SIGTERM (also SIGHUP on Unix), closing
every session first: `Run` returns when its context is done, whether or not its
input is still open.

### Environment

The adapter starts claude in the environment it was given, minus every `FLOCKDECK_`
and `PERCH_` variable. The client must pass what claude reads its login from
(`HOME` or `USERPROFILE`, `APPDATA`); the client's default environment has none of
them. `FLOCKDECK_CLAUDE_BIN` names the executable when it is not on `PATH`.
MCP servers in `session/new` are written to `mcp.json` in a private temporary
directory (`flockdeck-mcp-*`) for `--mcp-config`. The directory is removed when the
session ends, and a directory a week old, left by an adapter that was killed, is
removed at the next start. claude's account connectors still load.

### /compact, /clear and /context

Checked against claude 2.1.312 on 27 Oct 2026, in a throwaway directory: one prompt
asking for one word, then `/compact`, `/context`, `/clear` and one more word, each sent as
a `user` line on the same process.

- `/compact` runs. claude writes `system` `status: "compacting"`, then `system`
  `compact_boundary` with `compact_metadata` (`trigger`, `pre_tokens`, `post_tokens`),
  then replayed `user` lines holding the summary and
  `<local-command-stdout>Compacted </local-command-stdout>`, then a `result` with an
  empty `result` and `num_turns` 0. The adapter turns the `compact_boundary` line, which
  claude also writes for an automatic compaction, into the ACP `compaction` update with
  the message "Context compacted: 30,874 to 10,612 tokens." (the two numbers are claude's,
  the words are ours). The `user` lines and the command output are not forwarded.
- `/context` runs and answers as an assistant message, which is shown as a reply.
- `/clear` runs. claude writes a `conversation_reset` line (`trigger: "clear"`,
  `new_conversation_id`), the next `init` line carries a new `session_id`, and the
  conversation that follows has no memory of the one before. The adapter sends a
  `flockdeck_notice` update with the code `new_conversation`, which the bridge shows as
  "Started a new conversation." The ACP session id the client prompts with stays the old
  one: ACP has no update that changes it, and `adopt` only makes the new id reachable.
  The adapter also sends a `flockdeck_session` update with `newSessionId`, once for each
  id claude reports that differs from the last. The structured runtime
  (`noteMoved`) takes it only from the `claude` adapter of the manifest and only when the
  id is a UUID, and returns it from `SessionID()`, the id a layout saves. A pane restored
  after a `/clear` resumes the new conversation. Prompts in the running pane still go to
  the id it has. The next `/clear` moves the id again; the alias of the one before is
  dropped.
  The runtime calls `Options.OnSessionMoved` when the id to save changes (outside its
  lock); the workspace passes it to the server, which saves the layouts at once, so a
  crash right after `/clear` does not restore the old id. If the runtime holds an id from
  an unanswered reopen, the moved id wins over it.

  A fork or a rewind (T215) that makes claude report another session id would send the
  same update. That is intended only if the pane goes on following the new conversation;
  a fork that leaves the pane on the original conversation must not send it, or a
  restored pane would resume the fork.

So the slash menu offers all three, and no Flockdeck-side "new conversation" action was
added. If a later claude stops honouring `/clear` under `-p`, the entry has to be hidden
for this agent and a Flockdeck action added.

### Installed version and the pin

The adapter drives a control protocol that claude does not document. It was tested with
one claude version, the `testedWith` of the `claude` entry in
[`adapters.json`](../internal/acp/manifest/adapters.json) (2.1.311), and takes every version with
the same major and minor (2.1.x) as tested.

At the first `session/new` or `session/resume` of an adapter process, the adapter runs
`claude --version` (10 second limit, at most 4 KiB read, once per process) and compares the
major and minor with the manifest's. When they differ, or the output has no version, or
the command fails, the session still starts and the client gets a `flockdeck_notice` update
with the code `untested_version`. The bridge shows it in the conversation as "This Claude
version has not been tested with Flockdeck". The text is the bridge's; the version string
is only logged.

If claude's answer to the `initialize` control request is an error, or does not come within
the control timeout (30 seconds), `session/new` fails with "Claude Code is unavailable",
claude is ended, and nothing else is guessed. The handshake is the one place where a
changed control protocol shows up at once.

To move the pin: install the new claude, run the canary by hand
([The canary](#the-canary)) and the adapter tests against it, then change `testedWith` (and
`testedOn`) of the entry. The tested series follows from `testedWith`; there is no second
list. The weekly canary also runs the newest claude, so a protocol change shows up there
before a user's claude updates.

## Rewind and fork

The adapter advertises `agentCapabilities._meta["flockdeck.dev"] = {"rewind":1}` and `sessionCapabilities.fork`.
`session/prompt` takes a turn token in `_meta["flockdeck.dev"].turn`, which becomes the user message's
`uuid`; the adapter keeps the uuid of the last assistant message of each turn that ended by itself.
`_flockdeck/forkpoint` answers `{at}`, the previous turn's last uuid. `session/fork` starts claude with
`--resume SRC [--resume-session-at AT] --fork-session --session-id NEW`. `_flockdeck/rewind` starts a
second claude with `--resume ID --resume-session-at AT` on the same id, puts the mode and model back, and
ends the first; it is refused while a turn or a queued prompt is there. The turns are kept in memory, so
turns from before an adapter restart cannot be named.

## Not done

- `session/resume` starts claude with `--resume <id>` in place of `--session-id <id>` and
  returns the id it was given. The id must be a UUID and not open already; anything else is
  refused before claude starts. Observed against a real claude 2.1.311 on 27 Oct 2026 (a
  one-word prompt with `--session-id`, then a one-word prompt with `--resume` on the same id):
  both runs reported the same `session_id` in every line, so claude keeps the id on resume.
  With `--resume` and an id it has no conversation for, claude exits with status 1 within
  about 1.4 seconds and writes "No conversation found with session ID" to stderr, so the
  adapter reports an unavailable agent and the pane starts a new session. claude's `init`
  line, and with it the id it reports, arrives only once the first prompt is sent, and the
  `initialize` control answer has no id, so the id cannot be put in the `session/resume`
  result. The adapter adopts an id that differs from the one asked for (the session is
  also reachable under it and a resume of it is refused as open), but ACP has no update
  that changes a session's id, so the client keeps the one it has. Not checked: whether the
  conversation is found when the cwd differs from the one it was made in.
- The "already open" check is per adapter process. Two panes saved with the same id
  (a copied layout) would run two `claude --resume` on one conversation, and two
  simultaneous requests can both start claude before the second is closed at registration.
- Turns claude starts on its own, such as a finished background task. The adapter
  notices the turn from its `init` line and forwards its updates as `session/update`
  while no prompt is running. A prompt that arrives meanwhile waits for that turn's
  result before it is written to claude, so it gets a result of its own (a cancel
  answers it cancelled, and interrupts claude, which is ended if it does not stop
  within 15 seconds). If claude's own turn sends nothing for 120 seconds, prompts
  stop waiting for it. What is still open: ACP has no turn without a prompt, so
  the client has to decide where such updates show, and a turn that starts in the
  few milliseconds between the check and the write is taken for the prompt's.
- Steering a running turn with a second message (above).
- Approving a plan with automatic acceptance of edits: Allow on `ExitPlanMode` is a plain
  allow, and a mode change claude reports afterwards is passed on.
- `bypassPermissions` mode, API-key login through `authenticate`, delegating Bash and file
  access to the client's terminal and file system.
- Tool output while a tool runs: claude sends it at completion only.

## Signing in from Flockdeck

A new user with no terminal habit could not sign in: `authMethods` was empty and the pane said
to run `claude` once somewhere else. The adapter now lists one method and implements `authenticate`
for it (T232, API 1.24).

Routes considered:

| Route | Verdict |
| --- | --- |
| Run `claude auth login` as a process of the adapter, take the address it prints, wait for it to exit | Chosen. `claude auth login` exists in 2.1.312 (`--claudeai` is the default, `--console`, `--sso`, `--email`). The login and where it is stored stay claude's. Flockdeck sees an address and an exit code. |
| Ask the client for a terminal (ACP `terminal` capability, or a terminal-auth hint in the method) | Needs the terminal this task removes. The client advertises `terminal: false`. |
| Run the OAuth flow in Flockdeck (own client, PKCE, token exchange) | Flockdeck would hold a credential and depend on claude's private client id and redirect rules. Refused by the rules. |
| A key or token field in Flockdeck | Refused by the rules. An API key already goes in Settings or `flockdeck keys set claude-acp`; that is a different path and stays. |
| Start the login from the host, not the adapter | The host would need each agent's command line: a branch on agent type. The adapter already knows `claude`. |

How it runs:

1. `initialize` lists `{id: "flockdeck-login", name: "Sign in with the agent's login command"}`. The
   text is fixed.
2. A pane in the phase `needs setup` whose methods include that id offers a Sign in button
   (`structuredSignIn`). The host starts a second copy of the adapter, calls `initialize`, then
   `authenticate` with that id. The call waits for the command, up to six minutes.
3. The adapter starts `claude auth login` in a process tree it can end (job object on Windows,
   session on Unix), with the adapter's environment, and reads its output line by line.
4. The first address in the output that passes the checks below is sent once as the extension
   notification `_flockdeck/signin_url` with `{url}`. The host checks it again and the pane's state
   carries it as `signIn.url`; the window draws a link with fixed words.
5. When the command exits with status 0 the adapter answers `authenticate` with `{}`. Any other end
   (a failure, the five minute timeout, a cancel) is the fixed error "the sign-in did not finish".
6. After an answer of `{}` the server restarts the pane, which starts claude with the login it now
   has. Nothing asks claude whether the login worked: if it did not, the first prompt asks for a
   login again and the pane is back in the same state.

Rules the code keeps (tests in `internal/acp/agentlogin`, `claudeacp/login_test.go`,
`structured/signin_test.go`, `server/structuredsignin_test.go`, `ui/test/signin.test.mjs`):

- The address is the only string from the login command that goes anywhere. The rest of its
  output is read and dropped; it is not logged, not in an error and not in a notification.
- The address must be `https`, have no user name or password, no port, a host that is not an IP
  address, no control, space, format or non-ASCII character, and be at most 2048 bytes. The adapter
  also requires the host to be exactly one of `claude.ai`, `claude.com`, `console.anthropic.com`
  or `platform.claude.com`: a subdomain or a sibling domain of those (`www.claude.ai`,
  `anthropic.com`, `claude.ai-evil.com`, a percent-encoded host) is refused. When the command prints
  several addresses, the first one on the list is used. The live check saw `claude.com`. The host
  repeats every rule except the host list (it does not know the agent's list) and the window repeats
  them again before it makes a link. "A host the adapter allows" holds for Flockdeck's own adapters
  only: the host starts a sign-in only for an adapter whose manifest command is `flockdeck` itself,
  and takes the address only from a notification that follows its own `authenticate` call. A foreign
  agent that lists `flockdeck-login` is never asked to authenticate with it, and its notification
  is ignored.
- A line the command leaves without a line break (a prompt that waits for the browser) is taken as
  a line after 200 ms without more output, so the address is found while the command still waits.
  Without that, a prompt that never ended its line would leave the pane at "starting" until the
  timeout.
- The command runs in the temp directory, not the project's, so it reads and writes nothing there.
  Its environment is the adapter's without Flockdeck's own variables (`FLOCKDECK_*`, `PERCH_*`) and
  without anything named like a key or token (`*API_KEY*`, `*AUTH_TOKEN*`). The host does not give the
  sign-in's adapter the stored API key at all, and does give it `HTTP_PROXY`, `HTTPS_PROXY`,
  `NO_PROXY` and `ALL_PROXY` from Flockdeck's own environment, since the login needs the person's
  network. The home variables stay: that is where claude keeps the login.
- The host starts the sign-in's adapter without the permission probe the pane's own start runs: that
  check starts the real agent and writes the pane's permission note, and a sign-in must do neither.
- One sign-in per agent at a time across panes, in the server: a second pane of the same catalog
  entry is refused with a fixed notice until the first ends or is cancelled, since two login commands
  would write the same stored login.
- A command that exited 0 counts as signed in even if a cancel came at the same moment. The server
  restarts the pane only if the runtime that started the sign-in still holds the pane: a pane the
  person restarted or closed while the page was open is left as it is, and its sign-in is ended.
- Known gap on Unix: if the adapter itself is killed with SIGKILL, nothing runs to end the login
  command, which keeps waiting for its browser callback until it times out on its own. On Windows the
  job object ends it. A closed pane, a restart and a normal exit end it first.
- One sign-in at a time in an adapter (a second `authenticate` is answered "a sign-in is already
  running") and one per pane. The timeout is five minutes in the adapter and six in the host.
- Cancel (`structuredSignInCancel`), closing or restarting the pane, and the adapter's stdin
  closing all end the command and what it started before they return. Tests check that a process
  the command started stops writing after a cancel, a timeout and a closed adapter.
- Desk only, like every structured command: refused to a window reached through the relay and to a
  viewer. Nothing is stored by Flockdeck.

Checked live on 28 Oct 2026 against claude 2.1.312, signed out, with the output piped as the
runner pipes it (stdin, stdout and stderr all pipes). The home, `CLAUDE_CONFIG_DIR`, `APPDATA` and
`LOCALAPPDATA` were a throwaway folder, no sign-in was completed and the address was not opened:

- The address is printed on stdout, about 0.2 seconds after start, with no terminal needed. Nothing
  went to stderr.
- It is two plain lines: `Opening browser to sign in…` and `If the browser didn't open, visit: <address>`; the card says it in the same order ("If the browser did not open, use this link").
  No escape sequences, no OSC 8 link, no wrapping; about 500 bytes, well inside the 2048 byte cap.
- The host was `claude.com`, which the allowlist has.
- claude opens the system browser itself. It honours `BROWSER`: with `BROWSER` set to a program
  that does nothing, that program was run and no browser started. The adapter does not set it, so on
  a real run the browser opens by itself and the link on the card is the fallback.
- The command keeps running, waiting for the browser's callback, and does not read stdin. It was
  ended with its tree after 15 seconds; no process was left.

What is still not known: that the command exits with status 0 only after a completed sign-in, and
what it does with an expired or abandoned one. Both are read from the exit status, and the pane
restarts and asks again if the login is not there.

## The catalog entry

`claude-acp` ("Claude Code (structured)", in `internal/agent/builtin.go`) is the
structured entry for this adapter. It is hidden, so the picker does not offer it, and
it can still be started by id, which is what a saved layout and the canary do. It stays
hidden until the end-to-end check that follows this task says otherwise.

| Field | Value |
| --- | --- |
| adapter | `claude` |
| auth | the person signs in on claude's own page, and Flockdeck holds no credential. Preferred: the Sign in button of the Agent info panel (see "Signing in from Flockdeck"). Fallback, and the text beside the login state: "Run `claude` once in a terminal and sign in with your Claude subscription, or store an Anthropic API key in Settings." |
| key slot | `api: {wire: "anthropic", keyEnv: ["ANTHROPIC_API_KEY"]}`. Settings and `flockdeck keys set claude-acp` store a key, and it is set in the adapter's environment as `ANTHROPIC_API_KEY` when the pane starts, and nowhere else. `agents.json` cannot add or change a slot |
| passEnv | `HOME`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA` and the four `XDG_*` directories, where claude keeps its login. `ANTHROPIC_API_KEY` is not passed from Flockdeck's own environment: a key exported in the shell for another tool does not reach the pane, only the stored one does |
| MCP | on: the Flockdeck MCP server is sent in `session/new` and `session/resume`, and the adapter writes it to a private `mcp.json` for `--mcp-config` |
| unavailable | when `claude` is not on PATH. A `flockdeck` on PATH proves nothing, since the adapter is a subcommand of the running program, so the check looks for `claude`. The pane then says "the agent is unavailable" and nothing else: no program name, no stderr |

A restored pane reopens its session through `session/load`. The adapter advertises
`loadSession: true` and `sessionCapabilities.resume`, so the pane's runtime calls
`session/load` with the id it saved. The adapter replays the stored conversation (next
section), then starts claude with `--resume <id>` as `session/resume` does.
`TestInteropRestoredPaneResumes` drives this with the app's client over a child process.
An id claude does not know makes the load fail, the runtime then tries `session/resume`
(which fails the same way), and the pane starts a new session with the notice "Could not
resume, started a new session."

### Session load and the transcript

claude sends no history when it resumes, so the adapter reads it from claude's own stored
session: `<config dir>/projects/<project>/<session id>.jsonl`, where the config dir is
`CLAUDE_CONFIG_DIR` in the environment claude gets, else `.claude` in the home directory
(`Options.HistoryDir` overrides it). The project directory is claude's spelling of the cwd
(every character other than a letter or digit becomes `-`). If the file is not there, the
project directories are searched in name order for the id; a root with more than 5000
entries is not searched. A symbolic link or junction, for the file or its directory, is not
followed. claude shortens a path over 200 characters and adds a hash to it; that is not
reproduced, so such a project is found only by the search. The session id is parsed as a
UUID first, so it cannot carry a path.

claude does not document the format. It was read from about 116,000 user lines in the files
on the development machine (claude 2.1.x, 27 and 28 Oct 2026), and the reader skips what it
does not know, so a format change shortens the replay and nothing else. `session/load` never
fails because of the history: no file, an unreadable file or an empty replay still opens the
session.

The replay is sent as `session/update` calls before the answer, for the id the client asked
with, so the client marks them as replay and the bridge builds the transcript from them
(`docs/structured-pane.md`, Resume). The adapter sends it before it starts claude and before
the session is registered, so nothing claude does on its own and no prompt can be taken for
part of it. No API change is needed. What is replayed:

- `user` lines with a string, or text and image blocks (an image shows as "[image]"), as
  `user_message_chunk`, when the line is the person's. `origin.kind` says whose it is: `human`
  or no `origin` is the person's; `peer` (a message from another session) and
  `task-notification` are not replayed.
- A prompt typed while a turn runs is stored only as an `attachment` line whose
  `attachment.type` is `queued_command`. It is replayed when `commandMode` is `prompt`,
  `isMeta` is not set and the origin is the person's. The `queue-operation` lines (enqueue,
  dequeue, remove) are bookkeeping and are not read. Checked: of 274 queued prompts from the
  person in the local files, 273 have no `user` line of their own.
- Text claude wrote is recognised by its element: a tag with a dash in its name at the start
  of the text, such as `command-name`, `command-message`, `command-args`,
  `local-command-stdout`, `local-command-caveat`, `bash-input`, `bash-stdout`, `bash-stderr`,
  `task-notification`, `system-reminder` and `user-prompt-submit-hook`. Those elements are
  stripped from the start of the text, and what is left is replayed (nothing, usually). A
  `command-name` element is replayed as the slash command, `/name args`. A tag without a
  dash is the person's text and is kept (`pasted_content`, which wraps a paste, is the one
  that occurs in the files; plain HTML is another). Tags seen in user lines in the local
  files: task-notification, pasted_content, bash-input, bash-stdout, local-command-caveat,
  command-name, local-command-stdout, command-message, system-reminder.
- Not replayed: lines marked `isMeta`, `isSidechain` (a helper agent's), `isCompactSummary` or
  `isVisibleInTranscriptOnly`; tool results (they go with their call); text that starts with
  "Caveat: The messages below" or "[Request interrupted".
- `assistant` lines as `agent_message_chunk` for text, `agent_thought_chunk` for thinking
  (claude stores most thinking as an empty string with a signature, so there is often none)
  and one `tool_call` for each `tool_use`, built by the same `describeTool` as a live turn.
  The tool's result is folded into that call (status `completed` or `failed`, the output, or the
  diff it was announced with for an edit), so a call and its result are one entry and one is
  never dropped without the other. A result with no call in the file is dropped. TodoWrite is
  not replayed, since a plan is current state.
- Never shown as the agent's words: a line with an `error` or `isApiErrorMessage`, and text
  from the model `<synthetic>` (claude's own local notes such as "No response requested.").

Limits: the last 16 MiB of the file are read (the newest part, which is what the entry and byte
caps keep; a first line that is cut is skipped); a line over 1 MiB is skipped without being
held; reading stops after 5 seconds; a message is cut at 64 KiB; at most 2000 entries and
about 4 MiB of text are sent, and when there is more the oldest are left out. When something
is left out the transcript starts with a notice, so the person is not shown a conversation that
looks whole: "Earlier parts of this conversation are not shown." (the file is longer than the
part read, or the caps dropped entries), "Later parts of this conversation are not shown."
(the time limit came first) and "Some parts of this conversation are not shown." (a line was too
long to read). They are `flockdeck_notice` codes `history_cut_earlier`, `history_cut_later`
and `history_cut_gap`, and the bridge holds the words. The bridge applies its own cleaning and
limits on top, as for live text. The replay takes the same path as Gemini's: it makes no
turn, is not typed, does not touch the queue or mail, and a prompt in it is a `prompt` entry
marked `replayed`. Two user lines with no reply between them merge into one prompt entry in
the bridge.

Rewind and fork (T215, branch `parity-rewind-fork`): claude's file is a tree, each line has a
`uuid` and a `parentUuid`, and the reader here replays the lines in file order. That is right
while a conversation only grows. A rewind (`--resume ID --resume-session-at AT`) or a fork
that leaves the old lines in the same file would put the abandoned branch in the replay too, so
once rewind lands the replay has to follow the `parentUuid` chain from the newest line back.
Not verified that `--resume-session-at` writes its new lines into the same file under the same
id; nothing in the local files was used to test it.

Tests: `history_test.go` and `history_rules_test.go` (`TestSessionLoad...`, one case per rule
in `TestSessionLoadRules`) and `TestInteropLoadReplaysMarkedAsReplay`. The tests that make a
link are skipped where the system does not allow a symbolic link; the directory test falls
back to a junction on Windows, the file test has no such fallback and is skipped there. The
name-order test cannot tell a sorted listing from an unsorted one on NTFS, which lists in
name order already.

## The canary

The `claude` job of [`acp-canary.yml`](../.github/workflows/acp-canary.yml) installs claude
(`@anthropic-ai/claude-code`, at the manifest's `testedWith` version and at the newest)
into a temporary folder with `--ignore-scripts`, builds `flockdeck`, and runs two checks
with a home, config and temp folders inside one new folder and an environment of `PATH`
and those folders: `TestCanaryInitialize` (the handshake with `flockdeck acp-adapter
claude`) and `TestCanaryClaudeSession` (`session/new` in a new working directory, then one
prompt). The runner has no login, so the prompt is expected to fail as "authentication
required". That shows claude still answers the adapter's `initialize` control request and
that the refusal is the one the app turns into its login state.

With `--ignore-scripts` the `@anthropic-ai/claude-code` package leaves a placeholder
`claude.exe` that exits 1, because its install script is what puts the native binary in
place. So the job installs the platform package
`@anthropic-ai/claude-code-linux-x64@<version>` (the same exact version, an
optionalDependency of the main package) with `--ignore-scripts`, puts its folder on PATH,
and fails the install step if `claude --version` does not run. No script runs. The job has
not been run, because Actions was off when this was written.

To run it by hand on a machine with claude installed and logged in:

```
go build -o <dir>/flockdeck .
mkdir <new-empty-dir>
FLOCKDECK_LIVE_AGENT_TESTS=1 FLOCKDECK_CANARY_LOGIN=1 \
FLOCKDECK_CANARY_BIN=<dir>/flockdeck FLOCKDECK_CANARY_ISO=<new-empty-dir> \
go test -tags acp_canary ./internal/acp/claudeacp -run TestCanaryClaudeSession -v -count=1
```

Without `FLOCKDECK_CANARY_LOGIN=1` the adapter gets a home inside the new folder and no
login. With it, the adapter gets the real home, so claude uses the login there and also
runs the user's own hooks and MCP servers; use it by hand only, never in the job. The
prompt asks for one word. The working directory is the new folder either way. The test
harness removes every `*_API_KEY` variable, so a key cannot be passed this way, and in
login mode claude writes a session for the new folder under `~/.claude/projects`.
`FLOCKDECK_LIVE_AGENT_TESTS` is needed because without it the harness puts a stub first on
PATH where `claude` is.

Observed on 27 Oct 2026 on Windows 11 with claude 2.1.311 and a flockdeck built from this
branch:

- No login (home inside the new folder): `initialize` and `session/new` succeeded, and
  the prompt failed with "Authentication required" after 1.4 seconds. This was seen on
  Windows only; whether a Linux runner with an empty home gives the same result is
  unverified, and the job's first run will show it.
- Login (the real home): `session/new` succeeded, and the one-word prompt returned the
  reply "ok" with stop reason `end_turn` after 3.6 seconds.

A first run, before the test took claude from the real PATH, used the harness's stub. The
adapter waited out its 30 second control timeout ("initialize: no answer") and the prompt
never returned. The test now fails if `claude` on PATH is the stub.

## Tests

`go test ./internal/acp/claudeacp` runs the adapter against a fake claude, which is
the test binary started again and replays lines recorded in the spike report. The
fake also answers control requests the way claude does and logs everything it
receives (`--version` answers from `FD_CLAUDEACP_VERSION`). Failure classes, the version
notice, the failed handshake and the compact and clear notices are in `fail_test.go` and
`version_test.go`. Two tests start the adapter as a child and drive it with `internal/acp`'s
client, one of them a restored pane resuming. `internal/agent` has the tests of the
catalog entry (key slot, passEnv, MCP flag, hidden, availability).
`TestCanaryClaudeSession` is the only test that starts a real claude, and only under the
`acp_canary` tag.
