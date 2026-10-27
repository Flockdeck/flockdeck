# Codex adapter route

Task T205. Research date 27 Oct 2026, from docs and source only; no Codex session was run. Source read: a shallow clone of openai/codex at commit c587d6e5 (27 Oct 2026; `codex-rs/app-server-protocol/schema/typescript`, `codex-rs/exec/src/exec_events.rs`, `codex-rs/app-server/README.md`, `sdk/`), plus the docs page at learn.chatgpt.com/docs/app-server (formerly developers.openai.com/codex/app-server). I read the first 100,000 of 143,804 characters of that page.

## Verdict

A Go ACP adapter is feasible, and the route is `codex app-server` over stdio. It needs no Node. Codex is Rust and the app-server is a subcommand of the same binary. The protocol covers everything ACP needs: sessions, streaming deltas, permission requests with allow/deny answers, interrupt, resume, image input, token usage and ChatGPT or API-key login. `codex exec --json` cannot support permissions or cancel and is not a candidate.

The upstream ACP adapter has gone the same way. The zed-industries README says development moved to `@agentclientprotocol/codex-acp`, "built on the Codex App Server" (search result summary, not read in the repo). That is evidence the route works, not a dependency we may take.

## Routes

### A. `codex app-server --listen stdio://` (recommended)

Transport. Newline-delimited JSON on stdin/stdout. The `"jsonrpc":"2.0"` field is omitted on the wire. Other listeners exist (`ws://IP:PORT`, `unix://PATH`, `off`); the docs call WebSocket experimental and unsupported. The Python SDK (`sdk/python/.../client.py`) uses exactly `codex app-server --listen stdio://` with pipes, so stdio is the exercised path.

Handshake. One `initialize` request per connection with `{clientInfo: {name, title, version}, capabilities: {experimentalApi, requestAttestation, optOutNotificationMethods, ...}}`, then an `initialized` notification. Anything earlier gets `Not initialized`. Experimental methods and fields need `experimentalApi: true`.

Schema. `codex app-server generate-json-schema --out DIR` and `generate-ts --out DIR` emit the schema for the exact binary version. The repo also checks in `schema/json` and `schema/typescript` (about 655 files under `v2`). Licence is Apache-2.0, so we may generate types from it. The docs say the schema is tied to the Codex version, with no separate protocol version number. We would pin a Codex version and regenerate on bump.

Client requests we would use:

| Need | Method |
| --- | --- |
| Start a session | `thread/start` {cwd, model, approvalPolicy, sandbox, config, baseInstructions, developerInstructions, ephemeral} |
| Resume | `thread/resume` {threadId, cwd, model, approvalPolicy, sandbox, excludeTurns}; also `thread/list`, `thread/read`, `thread/turns/list`, `thread/fork` |
| Prompt | `turn/start` {threadId, input: UserInput[], cwd, approvalPolicy, sandboxPolicy, model, effort, outputSchema} |
| Send input mid-turn | `turn/steer` {threadId, expectedTurnId, input} |
| Cancel | `turn/interrupt` {threadId, turnId}; returns `{}`, turn ends `status: "interrupted"` |
| Slash-like actions | `review/start`, `thread/compact/start`, `skills/list`, `thread/shellCommand` |
| Auth | `account/read`, `account/login/start`, `account/login/cancel`, `account/logout` |
| Models, limits | `model/list`, `account/rateLimits/read`, `account/usage/read` |

Server notifications we would translate: `thread/started`, `turn/started`, `turn/completed`, `item/started`, `item/completed`, `item/agentMessage/delta`, `item/reasoning/summaryTextDelta` and `textDelta`, `item/commandExecution/outputDelta`, `item/fileChange/patchUpdated`, `turn/diff/updated`, `turn/plan/updated`, `item/plan/delta`, `thread/tokenUsage/updated`, `serverRequest/resolved`, `error`, `warning`, `account/updated`, `account/login/completed`, `account/rateLimits/updated`. There are about 90 notification kinds; the rest (realtime audio, plugins, fs watch, Windows sandbox setup) can be ignored, and `optOutNotificationMethods` can silence them.

Items (`ThreadItem`, camelCase `type`): `userMessage`, `agentMessage` {text, phase}, `reasoning` {summary[], content[]}, `plan`, `commandExecution` {command, cwd, status, commandActions, aggregatedOutput, exitCode, durationMs}, `fileChange` {changes[], status}, `mcpToolCall` {server, tool, arguments, result, error}, `dynamicToolCall`, `webSearch`, `imageView`, `imageGeneration`, `collabAgentToolCall`, `subAgentActivity`, `contextCompaction`, `enteredReviewMode`/`exitedReviewMode`. Turn status is `completed | interrupted | failed | inProgress`, with `turn.error.codexErrorInfo` carrying cause strings such as `contextWindowExceeded`, `usageLimitExceeded`, `unauthorized`, `sandboxError`.

Permission and approval requests. These are server-to-client JSON-RPC requests that the client must answer:

- `item/commandExecution/requestApproval`: {threadId, turnId, itemId, command, cwd, reason, commandActions, networkApprovalContext, proposedExecpolicyAmendment, proposedNetworkPolicyAmendments, kind: "command" | "writeStdin"}. Answer `{decision}` where decision is `accept`, `acceptForSession`, `decline`, `cancel`, `{acceptWithExecpolicyAmendment: ...}` or `{applyNetworkPolicyAmendment: ...}`.
- `item/fileChange/requestApproval`: {threadId, turnId, itemId, reason, grantRoot}. Decision is `accept | acceptForSession | decline | cancel`. The diff itself comes from the item's `changes` and `item/fileChange/patchUpdated`.
- `item/permissions/requestApproval`: {cwd, reason, permissions} for extra filesystem or network grants.
- `mcpServer/elicitation/request` and `item/tool/requestUserInput`: MCP forms and model questions to the user.
- `applyPatchApproval` and `execCommandApproval` are the legacy forms.

What triggers a request is set by `approvalPolicy` (`untrusted`, `on-request`, `granular{...}`, `never`) and the sandbox (`read-only`, `workspace-write`, `danger-full-access`; per-turn `SandboxPolicy` with `networkAccess` and `writableRoots`). Both can change per turn. The Python SDK also uses `approvalsReviewer: "auto_review"`, a Codex-side reviewer model that answers approvals instead of the client.

Resume. `thread/resume` by id loads the stored rollout from `CODEX_HOME` and rejoins a running thread. `excludeTurns: true` returns metadata only, with history paged through `thread/turns/list` and `thread/items/list`. Fork and revert also exist.

Image input. `UserInput` has `{type: "image", url}` (the code also has `fileId`), `{type: "localImage", path}`, and audio equivalents. Remote URLs are accepted; whether `data:` URLs are accepted is unverified (`app-server/src/image_url.rs` exists and I did not read it). The safe path for ACP image blocks is to write a temp file and send `localImage`.

Usage. `thread/tokenUsage/updated` gives `{total, last, modelContextWindow}`, each breakdown being `totalTokens, inputTokens, cachedInputTokens, cacheWriteInputTokens, outputTokens, reasoningOutputTokens`. There is no cost field. `account/rateLimits/read` returns plan windows (`primary`, `secondary`, `credits`, `planType`) and `account/usage/read` exists (not examined). For API-key use we would have to price tokens ourselves.

Login. `account/login/start` takes `{type: "apiKey", apiKey}`, `{type: "chatgpt"}` (returns `loginId` and `authUrl`; the app-server runs a local callback listener and `account/login/completed` fires when done), `{type: "chatgptDeviceCode"}` (returns `verificationUrl` and `userCode`, so no local listener is needed), `{type: "chatgptAuthTokens", accessToken, chatgptAccountId}` (host supplies tokens and answers `account/chatgptAuthTokens/refresh` requests), plus Bedrock variants. `account/read` returns `{account: {type: "chatgpt", email, planType} | {type: "apiKey"} | null, requiresOpenaiAuth}`. So ChatGPT subscription login works through the app-server, and Flockdeck can drive it from its own UI. Where saved credentials live (`~/.codex/auth.json` or the keyring) I did not check.

Stability. The docs mark the app-server command as experimental, with WebSocket explicitly unsupported for production. Methods are added and renamed often (`thread/revert` replaced `thread/rollback` per the README). Expect to pin and run a canary, as the plan already does for other adapters.

### B. `codex exec --json`

One process per turn. Prompt on argv or stdin; `--image FILE` (repeatable), `--output-schema`, `--ephemeral`, `--skip-git-repo-check`, `codex exec resume <id|--last>` with its own `--image`. Stdout is JSONL `ThreadEvent`: `thread.started {thread_id}`, `turn.started`, `item.started|updated|completed {item}`, `turn.completed {usage}`, `turn.failed`, `error`. Items are snake_case: `agent_message`, `reasoning`, `command_execution`, `file_change`, `mcp_tool_call`, `collab_tool_call`, `web_search`, `todo_list`, `error`. Usage at `turn.completed` is `{input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens}`.

Gaps. `exec.rs` sets `approval_policy: Never`, so there are no approval requests at all; the only choices are sandbox mode or `--dangerously-bypass-approvals-and-sandbox`. There is no message-level streaming (items arrive whole) and no cancel other than killing the process. Each turn pays process start and rollout reload. The TypeScript SDK wraps this route (`spawn codex exec --experimental-json`) and needs Node anyway. Fine for a fire-and-forget worker, but it fails the permission-card and parity requirements.

### C. `codex mcp-server`

Listed in the Codex docs as a way to run Codex as an MCP server (tools `codex` and `codex-reply`). I did not read it. It exposes whole-session calls with less event detail than the app-server and Codex's own app-server is the richer surface, so I did not pursue it.

### D. SDKs

TypeScript: wraps route B, Node only. Python (`openai_codex`): a typed stdio client for route A, with generated models from the app-server schema; good reference code for the client side (approval handling in `_approval_mode.py`, routing in `_message_router.py`). No Go SDK exists in the repo. Third-party Go clients: not searched.

## Mapping to ACP

| ACP | Codex app-server | Notes |
| --- | --- | --- |
| initialize, authenticate | `initialize` + `initialized`, `account/read`, `account/login/start` | Advertise auth methods: ChatGPT (device code or browser), API key. |
| session/new | `thread/start` | `cwd` direct. MCP servers from the ACP request likely go in `config` as `mcp_servers.*` overrides: unverified. |
| session/load, resume | `thread/resume` + `thread/turns/list` replay | Flockdeck's `internal/acp` client does not call this yet. |
| session/prompt | `turn/start` | Stop reason from `turn/completed`: completed to end_turn, interrupted to cancelled, failed to an error. `contextWindowExceeded` maps to max_tokens loosely. |
| session/cancel | `turn/interrupt` | Needs the `turnId` from `turn/started`; a cancel sent before it arrives must be held. Pending approvals get `cancel`. |
| agent_message_chunk | `item/agentMessage/delta` | |
| agent_thought_chunk | `item/reasoning/summaryTextDelta`, `textDelta` | |
| tool_call, tool_call_update | `item/started|completed`, `outputDelta`, `patchUpdated` | Kind from item type: execute, edit, fetch for web search, other for MCP. File edits map to ACP diff content. |
| plan | `turn/plan/updated`, `item/plan/delta` | |
| session/request_permission | The four server requests above | `accept` to allow_once, `acceptForSession` to allow_always, `decline` to reject_once, ACP cancel to `cancel`. |
| session modes | `approvalPolicy` + sandbox presets | Offer read-only, auto (workspace-write, on-request), full access. Changes apply on the next `turn/start`. |
| model, effort config | `model/list`, `model`, `effort` on `turn/start` | |
| usage_update | `thread/tokenUsage/updated` | used = last total, size = `modelContextWindow`. No cost. |
| available_commands | `skills/list` plus fixed `/review`, `/compact` | |
| image content block | `localImage` | Via temp file. |
| session/list | `thread/list` | |

## What an ACP adapter cannot translate

- `reject_always`. Codex has no persistent deny; only allow-side amendments (`acceptForSession`, execpolicy and network amendments). Offer allow_once, allow_always, reject_once.
- ACP client-side `fs/*` and `terminal/*`. Codex reads, writes and runs commands itself inside its own sandbox; it does not call back to the client. Flockdeck sees the results as tool calls only. If Flockdeck ever wants to own the terminal, Codex cannot.
- Cost in currency. Not reported. Only token counts and ChatGPT plan rate-limit windows.
- Questions the model asks the user (`item/tool/requestUserInput`) and MCP form elicitations have no stable ACP counterpart; map to a permission-style card with options, or decline.
- Mid-turn `turn/steer`, `thread/fork`, `thread/revert`, goals and queued turns are Codex extensions with no ACP method. They need Flockdeck-specific extension methods or are dropped.
- Per-turn `outputSchema`, `approvalsReviewer` auto-review and Guardian are Codex-only and would be config, not ACP features.
- MCP servers whose transport the Codex config does not support (ACP allows stdio, HTTP, SSE). Unchecked.

## Effort

The adapter would be a Go package that starts `codex app-server --listen stdio://`, speaks Flockdeck's ACP on its other side, and keeps a table of itemId to tool_call id and of pending server requests to ACP permission requests. Types: generate Go structs from the checked-in JSON schema, or hand-write about 40 of them. Estimated size: 2,000 to 3,000 lines plus tests, mostly the item translation and approval bookkeeping. A fake app-server that replays recorded JSONL gives a no-cost test harness; the Python SDK's `tests/app_server_harness.py` is a model.

## Risks and checks before building

1. Whether the app-server accepts messages carrying `"jsonrpc":"2.0"` (the wire omits it; Rust serde probably ignores the extra field). Test with a real binary.
2. A Codex binary without Node. The npm package is a launcher around a native executable, and release archives for Windows, macOS and Linux should exist on GitHub releases. Not checked. Flockdeck needs a way to find or download a pinned build.
3. Cancel races: `turn/interrupt` before `turn/started`.
4. Resume history replay: whether `thread/turns/list` gives enough to rebuild ACP updates.
5. `data:` URL support for images, and `config` overrides for MCP servers.
6. Windows: sandbox setup (`windowsSandbox/setupStart`, `readiness`) is a separate flow and may need a first-run step in Flockdeck.
7. ChatGPT login callback listener on loopback; prefer device code where Flockdeck is not on the same machine as the browser.
8. Protocol churn. The README is a running changelog with removals; pin and canary.
