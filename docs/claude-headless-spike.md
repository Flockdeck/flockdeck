# Claude Code headless spike (T183)

Question: can a Go ACP adapter drive Claude Code with no Node, using only the `claude` binary in `-p --input-format stream-json --output-format stream-json` mode?

Answer: yes. Everything an ACP agent needs has a wire equivalent in that mode. Permissions, interrupt, mid-turn messages, resume, images, MCP servers and slash commands all worked in real runs. The costs are that the control protocol is only lightly documented (the key flag is undocumented) and that the adapter has to build several ACP-side features itself (listed at the end).

## How this was run

- `claude` 2.1.311 (native binary, Windows), `--model haiku` (Haiku 5.5), the user's real Max login.
- 10 sessions that reached the API, each a few short turns, in an empty scratch directory outside any repo. Total spend was a few cents (a turn costs about $0.0015).
- A small Python script spawned the CLI, wrote JSON lines to stdin and logged every stdout line. FLOCKDECK_* env vars were stripped from the child. Nothing was printed or saved from `~/.claude`.
- `--setting-sources project,local` on most runs, so the user's global hooks did not fire. Run 1 also read the default settings for the init dump.
- The base command line was `claude -p --input-format stream-json --output-format stream-json --verbose`. `--verbose` is required with stream-json output.

Not tested: API-key login (no key used), `--permission-prompt-tool mcp__...` (the stdio path below covers the same need), AskUserQuestion and ExitPlanMode round trips, tool-input deltas under `--include-partial-messages`, hooks that block, project-scope `.mcp.json` auto-load (my first config file was invalid JSON; the `--mcp-config` path was tested instead).

## Capability table

| Capability | Result | Evidence |
|---|---|---|
| Stdin message schema | works | One JSON object per line: `{"type":"user","message":{"role":"user","content":"text"}}`. `content` may be a block array. Optional `"uuid"` is echoed back by `command_lifecycle` and `--replay-user-messages`. |
| Stdout event types | works | Seen: `system` (subtypes `init`, `status`, `permission_denied`, `thinking_tokens`, `task_started`, `task_updated`, `task_notification`, `background_tasks_changed`, `hook_started`, `hook_response`), `stream_event` (raw Anthropic SSE events, with `--include-partial-messages`), `assistant` (complete message, one per content block batch), `user` (tool results, replays), `result`, `rate_limit_event`, `control_request`, `control_response`, `control_cancel_request`, `command_lifecycle`. |
| Tool permission request | works, undocumented flag | Needs `--permission-prompt-tool stdio`. Without it the CLI denies silently: `{"type":"system","subtype":"permission_denied","tool_name":"Write","message":"Claude requested permissions to write to ...a.txt, but you haven't granted it yet."}` and the model is told "no". With it: `{"type":"control_request","request_id":"0c74...","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","input":{"file_path":"...\\a.txt","content":"hi"},"description":"a.txt","permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"toolu_017R..."}}`. MCP tools add `"mcp_server":{"name":"pingsrv","source":"dynamic"}` and a suggestion `{"type":"addRules","rules":[{"toolName":"mcp__pingsrv__ping"}],"behavior":"allow","destination":"localSettings"}`. |
| Answering a permission request | works | Allow: `{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{"behavior":"allow","updatedInput":<input>}}}`. Deny: `"response":{"behavior":"deny","message":"user said no"}`. Run: first Write allowed (a.txt created), second denied (b.txt absent), result lists it under `permission_denials`, model reported it honestly. |
| Cancel a pending permission | works | Sent `{"type":"control_request","request_id":"int-1","request":{"subtype":"interrupt"}}` while a `can_use_tool` was open. Output: `control_cancel_request` (retracts the open request), `control_response` `success`, then `result` with `"subtype":"error_during_execution","terminal_reason":"aborted_tools"`. |
| Cancel a running tool | works | Interrupt 6 s into a foreground `python time.sleep(40)`. Same sequence plus `task_notification` for the killed command, all within the same second. The tool result seen by the model was "The user doesn't want to proceed with this tool use", and a `[Request interrupted by user for tool use]` user message was appended. The process stayed alive; the next user message ("alive") ran normally. |
| Second message mid-turn | works, merged | Sent while a 15 s Bash command ran, with `--replay-user-messages`. Stream: `command_lifecycle {"state":"queued"}` at once; at the next tool boundary `state:"started"` and the replayed user line; the model answered both in one turn and emitted one `result` (`"result":"FIRST\nSECOND"`, `queued_turn_count:0`). So a mid-turn message steers the running turn. It does not start a second turn. A message sent while idle starts a normal turn. |
| Resume by session id | works | New process with `--resume 9ac8f59a-...`, asked what was said before: answered correctly. The `init` event and `result` carry the same `session_id`. `--fork-session` and `--session-id <uuid>` (pick the id up front) also exist; `--session-id` was confirmed (result `session_id` equals the given uuid). `--continue` and `--resume <name>` exist but were not run. |
| Slash commands | works | `/hello world` sent as an ordinary user message ran `.claude/commands/hello.md` and returned `HELLO-world`. `init.slash_commands` listed 55 names including custom ones; `init.terminal_slash_commands` lists 4 that are TUI-only. The `initialize` control request returns each as `{name, description, argumentHint, builtin}`. |
| Skills | works | A project skill `pingskill` appeared in `init.skills` (19 total) and "run pingskill" returned `PINGSKILL-OK`. |
| Hooks | works | A project `UserPromptSubmit` hook ran for each prompt (3 appends to the log file for 3 prompts). With `--include-hook-events` the stream carries `{"type":"system","subtype":"hook_started","hook_name":"UserPromptSubmit",...}` and `hook_response` with `stdout`, `stderr`, `exit_code`, `outcome`. |
| MCP servers | works | `--mcp-config <file> --strict-mcp-config` loaded a stdio server; `init.mcp_servers` showed `pingsrv` `connected`, tool `mcp__pingsrv__ping` was callable, permission asked via `can_use_tool`, answer "pong". Account-level claude.ai connectors (Docs, Sheets, Gmail) also load in `init.mcp_servers` unless `--strict-mcp-config` is set. A `mcp_status` control request exists. |
| Image input | works | User content block array with `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}` after a text block. A 16x16 red PNG was described as "Red". |
| Cost and tokens | works | `result` has `total_cost_usd` (cumulative for the process: 0.00156 after turn 1, 0.00302 after turn 2), `usage` (input, output, cache read/creation tokens, `iterations`), `modelUsage` per model with `costUSD`, `contextWindow`, `maxOutputTokens`, plus `duration_ms`, `duration_api_ms`, `ttft_ms`, `num_turns`, `permission_denials`, `terminal_reason`. A `get_context_usage` control request returns a category breakdown (system prompt 2502 tokens, tools 13821, ...). |
| Login type | works | `init.apiKeySource` was `"none"` with a subscription login, and the `initialize` response has `account.subscriptionType: "Claude Max"` and `apiProvider: "firstParty"`. `claude auth status` prints `authMethod`, `subscriptionType`. `rate_limit_event` carries five-hour and seven-day utilisation (`"utilization":0.04`). API-key login was not run; per the docs `ANTHROPIC_API_KEY` in the env would take over, and `apiKeySource` should then name the source. `--bare` is API-key only. |
| Mode, model and effort switching | works | Control requests `set_permission_mode` (`{"mode":"plan"}` returns `{"mode":"plan"}`) and `set_model` (`{"model":"haiku"}` returns `{"model":"claude-haiku-5-5"}`). `initialize` lists models (`default`, `opus`, `fable`, `sonnet`, `haiku`, ...) and the current mode. |
| Text streaming | works | With `--include-partial-messages`, `stream_event` lines wrap `message_start`, `content_block_start`, `content_block_delta` (`text_delta`), `content_block_stop`, `message_delta`, `message_stop`. Without it only whole `assistant` messages arrive. |
| Turn boundary | partial | The `result` line ends a turn. `init` is re-emitted at the start of every turn, not once per process. |
| Agent-initiated turns | partial | A background Bash task finished 45 s later with no user input: the stream emitted `task_notification`, a fresh `init`, and a new assistant turn. The adapter must keep reading after `result` and treat these as turns it did not start. |
| Process exit | works | Closing stdin ends the process with code 0 after pending work. |

## Event shapes worth copying

Init (trimmed):

```
{"type":"system","subtype":"init","cwd":"...","session_id":"9ac8f59a-...","tools":[...28],"mcp_servers":[{"name":"claude.ai Gmail","status":"pending"}],"model":"claude-haiku-5-5","permissionMode":"default","slash_commands":[...55],"apiKeySource":"none","claude_code_version":"2.1.311","agents":[...],"skills":[...],"plugins":[...],"capabilities":[...]}
```

Result (trimmed):

```
{"type":"result","subtype":"success","is_error":false,"result":"one","stop_reason":"end_turn","terminal_reason":"completed","session_id":"9ac8f59a-...","total_cost_usd":0.00156,"num_turns":1,"queued_turn_count":0,"permission_denials":[],"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":16829,"cache_creation_input_tokens":6973},"modelUsage":{"claude-haiku-5-5":{"costUSD":0.00156,"contextWindow":1000000}}}
```

Tool use and result arrive as ordinary messages: an `assistant` line with a `tool_use` block (`id`, `name`, `input`), later a `user` line with a `tool_result` block (`tool_use_id`, `content`, `is_error`). The `can_use_tool` request carries the same `tool_use_id`, so the adapter can match them.

## What an ACP translation layer has to invent

1. Permission options. ACP `session/request_permission` offers allow-once, allow-always, reject-once, reject-always. Claude gives allow or deny plus a `permission_suggestions` list. "Always" has to be built from the suggestion: either return it as `updatedPermissions` in the allow response (shape inferred from the SDK, not run here) or keep a per-session rule list in the adapter and answer matching requests without asking.
2. Tool-call lifecycle. ACP wants `tool_call` then `tool_call_update` with status, kind (read, edit, execute, ...), locations and diffs. Claude gives raw tool name and JSON input, then a `tool_result` text blob. Kind mapping, file locations, and edit diffs (Edit and Write inputs hold old/new text; the adapter computes the diff) are all adapter work. Streaming a tool's output while it runs is not available; output comes only at completion.
3. Text and thinking chunks. Map `stream_event` deltas to `agent_message_chunk` and `agent_thought_chunk`. Needs `--include-partial-messages`; thinking text comes redacted or empty in the run here (`"thinking":""` with a signature), so thought chunks may be empty.
4. Turn and stop reason. ACP `session/prompt` returns a stop reason. Derive it from `result`: `end_turn`, `max_turns` and so on from `terminal_reason`, `cancelled` from `error_during_execution` + `aborted_tools` after an adapter-sent interrupt.
5. Cancel. ACP `session/cancel` becomes an `interrupt` control request. The adapter must also answer any open permission request as cancelled on the ACP side (the CLI retracts it with `control_cancel_request`).
6. Mid-turn prompts. ACP has no queued-prompt concept. Decide whether a second `session/prompt` during a turn is rejected, queued by the adapter until `result`, or sent straight through (steers the turn, one `result`). Straight through works but then two ACP prompts get one stop reason, so the adapter has to track the count (`command_lifecycle` per `uuid` helps).
7. Unsolicited turns. Background-task completions start turns with no prompt. ACP has no equivalent of a turn without a `session/prompt`; the adapter can emit `session/update` notifications outside a prompt, or hold them. This needs a product decision.
8. Session lifecycle. `session/new` spawns one process per session (the process is the session). `session/load` is `--resume <id>`. ACP's `session/load` expects history replay as updates; Claude's resume sends none on stdout, so the adapter would have to read the session JSONL under `~/.claude/projects` and replay it. The format of that file was not examined here and is not a stable contract.
9. Modes, models, commands. Map ACP session modes to `--permission-mode` and `set_permission_mode`, models to `set_model`, `available_commands_update` to the `initialize` commands list plus skills. Custom slash commands need no translation: send them as text.
10. MCP servers. ACP passes MCP servers in `session/new`. Write them to a temp JSON file for `--mcp-config` (the flag takes files or strings) with `--strict-mcp-config` if claude.ai connectors should stay out. ACP http/sse servers map to Claude's config types.
11. Content blocks. ACP image and resource blocks map to Claude image blocks (base64 only; no URL form tested) and text. Embedded text resources and file links become text.
12. Auth. ACP `authenticate` has no wire call here. Login is out of band (`claude auth login`, `claude auth status` for a check). The adapter can read `init.apiKeySource` and `initialize.account` to show which login is in use. It must not read credential files.
13. Usage. ACP usage updates come from `result.usage` and `total_cost_usd` (cumulative per process, so the adapter subtracts for a per-turn figure; `model_usage_since_last_result` also exists).
14. Terminal and filesystem. ACP lets a client offer `terminal/*` and `fs/*` to the agent. Claude Code runs its own Bash and file tools and has no hook to delegate them to the client, so the client sees only the tool-call updates, not an ACP terminal it owns. (Not tested; inferred from the absence of any such flag in `--help`.)

## Risks and blockers

- The stdio control protocol is not covered by the public CLI reference. The `stdio` value for `--permission-prompt-tool` and the `control_request` subtypes (`can_use_tool`, `interrupt`, `initialize`, `set_model`, `set_permission_mode`, `mcp_status`, `get_context_usage`) were found by experiment and match what the Agent SDK sends. They can change between CLI versions. Pin a tested version range and run these probes in CI.
- The documented alternative for permissions is an MCP tool named by `--permission-prompt-tool mcp__server__tool`. That would need the adapter to host an MCP server (possible in Go over stdio or HTTP) and is a heavier path than `stdio`.
- Project-scope `.mcp.json` loading under `-p` was not verified (my config was invalid). If the adapter relies on settings files for MCP, test that separately.
- `claude.ai` account connectors load by default and show `pending` at init; they can race a first turn. Use `--strict-mcp-config` when determinism matters.
- Resume history replay (item 8) has no wire support.
- Windows: the Bash tool refused a foreground `sleep` and moved it to the background on its own. That is model and harness behaviour, but it shows tool commands can change between what the user asked and what runs, which the permission UI must display from `input`, not from the prompt.
