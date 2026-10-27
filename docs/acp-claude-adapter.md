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
arrived at the same moment. `AskUserQuestion` is denied with a message telling the
model to ask in plain text. Any other control request from claude is answered with
an error so it does not wait.

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
  "Started a new conversation." The ACP session id the client holds stays the old
  one: ACP has no update that changes it, and `adopt` only makes the new id reachable. A
  pane that is restored later resumes the old conversation, from before the `/clear`.
  That is not handled.

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

## Not done

- `session/load`. Advertised as `loadSession: false`. claude sends no history on stdout, so
  it would have to read the transcript under `~/.claude/projects` and replay it as updates,
  a file format that is not a stable contract.
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
- Questions (`AskUserQuestion`) and the plan-approval flow of `ExitPlanMode` beyond
  a normal permission card.
- `bypassPermissions` mode, API-key login and `authenticate`, delegating Bash and file
  access to the client's terminal and file system.
- Tool output while a tool runs: claude sends it at completion only.

## The catalog entry

`claude-acp` ("Claude Code (structured)", in `internal/agent/builtin.go`) is the
structured entry for this adapter. It is hidden, so the picker does not offer it, and
it can still be started by id, which is what a saved layout and the canary do. It stays
hidden until the end-to-end check that follows this task says otherwise.

| Field | Value |
| --- | --- |
| adapter | `claude` |
| auth | external: Flockdeck never signs in. The text beside the login state is "Run `claude` once in a terminal and sign in with your Claude subscription, or store an Anthropic API key in Settings." |
| key slot | `api: {wire: "anthropic", keyEnv: ["ANTHROPIC_API_KEY"]}`. Settings and `flockdeck keys set claude-acp` store a key, and it is set in the adapter's environment as `ANTHROPIC_API_KEY` when the pane starts, and nowhere else. `agents.json` cannot add or change a slot |
| passEnv | `HOME`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA` and the four `XDG_*` directories, where claude keeps its login. `ANTHROPIC_API_KEY` is not passed from Flockdeck's own environment: a key exported in the shell for another tool does not reach the pane, only the stored one does |
| MCP | on: the Flockdeck MCP server is sent in `session/new` and `session/resume`, and the adapter writes it to a private `mcp.json` for `--mcp-config` |
| unavailable | when `claude` is not on PATH. A `flockdeck` on PATH proves nothing, since the adapter is a subcommand of the running program, so the check looks for `claude`. The pane then says "the agent is unavailable" and nothing else: no program name, no stderr |

A restored pane resumes through `session/resume`. The adapter advertises
`sessionCapabilities.resume` and `loadSession: false`, so the pane's runtime calls
`session/resume` with the id it saved, and the adapter starts claude with `--resume <id>`.
`TestInteropRestoredPaneResumes` drives this with the app's client over a child process.
An id claude does not know makes the resume fail, and the pane starts a new session with
the notice "Could not resume, started a new session."

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
