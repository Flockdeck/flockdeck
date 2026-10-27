# Claude Code adapter

Flockdeck has its own ACP adapter for Claude Code. It is the `acp-adapter claude`
subcommand of the `flockdeck` binary: `flockdeck acp-adapter claude` speaks ACP on
stdin and stdout and starts one `claude` process per session. It needs nothing
except the `claude` executable. The manifest entry (`claude` in
[`adapters.json`](../internal/acp/manifest/adapters.json)) is hidden, so it is not in the
table on [Structured-agent adapters](acp-adapters.md) and the canary does not
try to install it.

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
the adapter was tested with (2.1.311).

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
with `/review` is sent as text, and claude runs it.

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
A failed turn is "the turn failed", a refused model or mode change is "the agent
could not change the model" or "...the mode", and a claude that exits mid-turn
fails the prompt with "the agent stopped during the turn". The detail goes to the
adapter's stderr only. A result that says the login is missing becomes error
`-32000` "Authentication required", which the client turns into its
authentication state.

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

## Not done

- `session/load`. Advertised as `loadSession: false`. claude sends no history on stdout, so
  it would have to read the transcript under `~/.claude/projects` and replay it as updates,
  a file format that is not a stable contract.
- Checking `session/resume` against a real claude. It starts claude with `--resume <id>`
  in place of `--session-id <id>` and returns the id it was given. Not checked: that claude
  keeps the same id when resumed (if it forks to a new one, the next resume of the saved id
  still works, but the saved id is not the live one), what it does for an id it has no
  conversation for (the adapter treats an exit as a failed start), and that the
  conversation is found when the cwd differs. Tested against the fake claude only. The id
  must be a UUID and not open already; anything else is refused before claude starts.
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

## Tests

`go test ./internal/acp/claudeacp` runs the adapter against a fake claude, which is
the test binary started again and replays lines recorded in the spike report. The
fake also answers control requests the way claude does and logs everything it
receives. One test starts the adapter as a child and drives it with `internal/acp`'s
client. No test starts a real claude.
