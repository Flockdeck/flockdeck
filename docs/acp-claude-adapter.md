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

Each card offers Allow, Always allow and Reject. "Always allow" is kept by the
adapter for the life of the session and is not written to any settings file.
For Bash and PowerShell it covers that exact command; for every other tool it
covers the tool, so Always allow on Edit allows every edit until the session ends.
A card answered "cancelled", or a request claude retracts, is denied or dropped.
`AskUserQuestion` is denied with a message telling the model to ask in plain text.
Any other control request from claude is answered with an error so it does not wait.

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

If `claude` cannot be started or exits during `session/new`, the error is
"Claude Code is unavailable". The reason goes to the adapter's stderr only. A
result that says the login is missing becomes error `-32000` "Authentication
required", which the client turns into its authentication state. A claude that
exits mid-turn fails the prompt.

### Environment

The adapter starts claude in the environment it was given, minus every `FLOCKDECK_`
and `PERCH_` variable. The client must pass what claude reads its login from
(`HOME` or `USERPROFILE`, `APPDATA`); the client's default environment has none of
them. `FLOCKDECK_CLAUDE_BIN` names the executable when it is not on `PATH`.
MCP servers in `session/new` are written to a temporary file for `--mcp-config` and
removed when the session ends; claude's account connectors still load.

## Not done

- Resume and `session/load`. Advertised as `loadSession: false`. Design: spawn with
  `--resume <id>`. claude sends no history on stdout, so `session/load` would have to
  read the transcript under `~/.claude/projects` and replay it as updates, a file
  format that is not a stable contract. Two cheaper options: support resume without
  replay (the pane keeps its own record), or replay from Flockdeck's recording.
- Turns claude starts on its own, such as a finished background task. The adapter
  keeps reading after `result`, so their updates are forwarded as `session/update`
  while no prompt is running, but their `result` is dropped. A result that arrives
  during a prompt would end that prompt early. ACP has no turn without a prompt;
  the client has to decide where such updates show.
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
