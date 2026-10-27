# T182: ACP for all agents, retire the pull inbox

Updated 27 Oct 2026 after the owner's decisions in [acp-direction.md](acp-direction.md). The plan below was written first; the notes marked "Superseded" say where a decision changed it. Stage numbers are the plan's own; the board tasks are ACP n = T182 plus n (ACP 2 is T184, ACP 13 is T195, and so on).

Superseded as a whole:

- Terminal panes stay as a fallback (section 3): no. Every agent runs over ACP and terminal-pane agents go away.
- Third-party adapters pinned with a canary, and Node 20+: no. Agents without native ACP get a Go adapter inside the flockdeck binary (Claude: T204, Codex: T206). The Node check is now a generic "agent unavailable" state (T207, API 1.15).
- `flockdeck chat` as an ACP server (stage 12): no. `flockdeck chat` is removed (T201) and API keys move to Settings (T202).
- `flockdeck inbox` stays, read-only, for terminal panes (section 2): no. The inbox, `flockdeck msg`, hook notices and typed wake are retired; the store stays as an audit log.
- Cursor, Aider: Cursor and Copilot CLI and Cline run directly over their native ACP; Aider and Amp are dropped ([registry survey](acp-registry-survey.md)).

Research since this plan: [Claude headless spike](claude-headless-spike.md), [Codex route](codex-adapter-route.md), [registry survey](acp-registry-survey.md), [parity matrix](acp-parity-matrix.md).

Read from origin/main at 4efb7311 (extracted from the flockdeck-man clone, since this checkout has no .git). Items marked "unverified" I did not check in code or on the vendor's page.

## What main has today

- `internal/acp` is a client for initialize, authenticate, session/new, session/prompt, session/cancel, permission requests and the update stream. It does not implement session/load or session/resume (the `client.go` header says so).
- `internal/structured` runs one agent per pane. It queues up to 16 prompts behind a running turn (`maxQueue`) and `paneSend` answers `queued`.
- `NewSession` accepts `mcpServers` and `Options.MCPServers` exists, but nothing in `internal/workspace` or `internal/server` fills it. The entry's `mcp` flag is read and then an empty list goes out.
- Mail to a structured pane was a text wake: opt-in per agent type, one prompt with the oldest unread message, marked read when the prompt is accepted. T187 replaced it with prompt delivery (`workspace/promptdelivery.go`): no opt-in, two content blocks, delivery states on the message. The agent gets no `FLOCKDECK_*` variables, so it cannot send mail at all.
- `team.Message` already has delivery states: queued, notified, woken, read (`internal/team/team.go`).
- Relay windows are refused every structured command and all conversation text.

## 1. Agent by agent

| Agent | Route | Real gap against the TUI |
| --- | --- | --- |
| opencode | Native (`opencode acp`), already a beta entry | Default permissions are "allow", worked around with launchEnv plus permprobe; a repo's own config can defeat it. No resume. |
| Gemini CLI | Native (`gemini --acp`), entry hidden | Needs Node 20+. Not checked end to end. No resume; client sends text only. |
| Codex | Third-party adapter (`@agentclientprotocol/codex-acp`, pinned 2.2.2), entry hidden. Superseded: Go adapter over `codex app-server` (T206) | Node 20+, adapter ships near-daily previews and has changed owner. Login is outside Flockdeck. |
| Claude Code | Superseded: Go adapter over `claude -p` stream-json (T204). Original note: third-party adapter `agentclientprotocol/claude-agent-acp`, npm `@agentclientprotocol/claude-agent-acp`, Apache-2.0, previews on a `preview` tag. The zed-industries `claude-code-acp` name is the same project moved (the repo page shows no deprecation notice). It wraps the Claude Agent SDK. | Largest gap of any agent; see below. |
| Cursor Agent | Native: `cursor-agent acp` (registry survey). Runs directly. | Its `cursor/ask_question` and `cursor/create_plan` requests need answers. |
| Aider | Superseded: dropped, no ACP route found. Original note: no ACP that I know of; shim or terminal pane. | `--message` exits after one turn; a shim needs a long-lived process or aider's Python API. Low value; keep terminal. |
| `flockdeck chat` API runners (anthropic, openai, google, compatible) | Superseded: removed (T201). Original note: add an ACP server mode to `flockdeck chat`. | None beyond what chat has now; it drops its screen-read wake rule. |
| Shell, other `agents.json` CLIs | Open: decision 1 allows no terminal fallback; the Shell pane is not decided. | n/a |

Claude Code gaps, from the adapter's page and the client:

- Slash commands: custom ones are supported. Built-ins such as `/compact`, `/model`, `/status`: unverified.
- Skills, plugins, hooks: the page does not mention them (unverified). Flockdeck's own Claude status comes from hooks today (`Caps.Hooks`, `ContextHook`); over ACP it comes from the update stream, the same path as opencode.
- MCP servers: supported per session. Servers from settings files: unverified.
- Permission modes: tool permission requests work. Plan and accept-edits modes are config options in the newer protocol version; unverified for v1. `opusplan` routing: unverified.
- Resume: the page lists it only for the experimental v2, and `internal/acp` has no session/load. Claude panes resume with `--resume` today. This is a hard gap.
- Image input: the adapter supports it; `Client.Prompt` sends text only, so Flockdeck must add content blocks.
- Cost display: `usage` already carries cost when the agent reports it.
- Auth: unverified whether a Claude subscription login works through the Agent SDK or only an API key. This decides whether most users can use it. Check before build work.
- Trust dialog, `--settings` injection, the session-file idle check and the transcript view all go away or change.

## 2. Mail over ACP

Delivery:

1. `team.Send` writes the message (state queued) and hands it to the target pane's runtime.
2. The runtime sends it as a `session/prompt`, or queues it behind the running turn (existing queue, 16 deep; spill to the store when full).
3. State delivered = the prompt was accepted and the turn began. The stop reason at turn end is stored on the message: `end_turn` is handled; `cancelled` or `refused` returns it to queued with a retry count. If the agent exits mid-turn, the message stays queued and is redelivered after restart.
4. The shell sense of "read" goes. States become queued, delivered, turn ended. "Acted on" cannot be known, so do not claim it. The stop reason is the honest ack.

Framing: the prompt starts with a fixed prefix naming the sender, as today, in a content block separate from the body. This keeps the guard against agent-written text being taken as the user's.

Store: keep `internal/team`'s store as an audited log (id, from, to, text, state, timestamps, stop reason, session id). It already feeds the team graph. To fix "mail is separate from agent chat", write each delivered message as a conversation entry in the receiver and a sent entry in the sender. (Superseded: the inbox is retired.) `flockdeck inbox` stays, read-only, for terminal panes.

Agent-to-agent send without a shell: Flockdeck provides an MCP server, started by the agent from `mcpServers` in session/new, exposing `send_message(to, text)` and `list_peers` (later task tools). It reaches Flockdeck over a per-pane token on a local socket, so pane identity comes from the token and not from the model. This needs MCP support in the adapter: opencode and the Claude adapter list it; Gemini and Codex are unverified. An adapter without MCP gets no send tool; parsing model output as commands is not acceptable, so that agent stays a terminal pane or receive-only.

An MCP tool call passes the normal permission card. Pre-allow `send_message` in the pane's session config and ask for anything else.

## 3. What stays

Superseded: see the notes at the top. Kept for the record.

- Terminal panes keep the current stack: inbox store, `flockdeck msg`, hook notices, typed wake lines, screen rules. Nothing is deleted until the last agent needing it is gone.
- Cursor Agent, aider and unknown `agents.json` CLIs stay terminal-only until a route is confirmed.
- A tab can mix both pane kinds. The team graph uses one delivery-state vocabulary for both.
- The relay is unchanged in this plan. Phones keep seeing roster state only.

## 4. Stages

All tasks keep `structuredPanes` opt-in until task 13.

| # | Title | Role | Deps | Size |
| --- | --- | --- | --- | --- |
| 1 | Spike: Claude over claude-agent-acp | researcher | none | S |
| 2 | Decision doc: auth, licence, Node, TUI loss | planner | 1 | S |
| 3 | Fill `mcpServers` from the workspace | coder | none | M |
| 4 | Flockdeck MCP server: send_message, list_peers | coder | 3 | M |
| 5 | Delivery states over prompts, store as audit log | coder | 3 | M |
| 6 | Mail entries in the conversation view | coder | 5 | S |
| 7 | session/load and resume for structured panes | coder | 1 | L |
| 8 | Image and content-block prompts | coder | none | S |
| 9 | Claude entry in the manifest and canary | coder | 1, 2 | M |
| 10 | Check Gemini and Codex end to end, unhide | tester | 4 | M |
| 11 | Cursor Agent ACP check | researcher | none | S |
| 12 | `flockdeck chat` as an ACP server | coder | 4 | L |
| 13 | Default to structured for ACP-capable agents, terminal opt-out | coder | 7, 9, 10 | M |
| 14 | Retire typed wake for moved agents | coder | 13 | S |

Bodies:

1. Install claude-agent-acp in a scratch directory and run one session through the existing client. Record login method, slash commands, hooks and skills from settings, MCP, permission modes, resume, images, cost. Output is a table in `docs/`.
2. Write the user's decision list with the spike's facts.
3. `Options.MCPServers` is never set. Build the list per pane (Flockdeck's server plus the user's), env scrubbed, honouring the entry's `mcp` flag.
4. A stdio MCP server inside the flockdeck binary. Pane identity from a token passed in that server's own env entry, not the agent's. Tests: spoofed pane id refused, rate limit, the 8 KB cap of `msg send`.
5. Add delivered and turn-ended states and the stop reason to `team.Message`; redeliver on restart; stop marking read on accept in `wake_text.go`. Update schema and docs together.
6. Write received and sent mail as conversation entries. The relay still gets none.
7. Implement the load and resume calls, replay handling, and the saved session id per pane.
8. Extend `Client.Prompt` to content blocks; gate on agent capability.
9. Add the adapter to `adapters.json` pinned, Node 20+, with a permission launch check and a canary job. Hidden until 13.
10. Run each adapter through the real-agent tests opencode has.
11. Read Cursor's CLI docs, test, and report native, shim or none.
12. Add `flockdeck chat --acp` so API runners join the same path.
13. Flip the picker default per agent once its gaps are closed; the terminal entry stays.
14. Remove screen-reading wake rules for moved agents only.

## Risks

- Prompts as an instruction channel: mail from another agent arrives as a user turn in an agent that can run commands. Keep prefix, caps and the per-agent opt-in until the MCP `send_message` permission model is settled.
- Adapter churn: Codex's adapter has changed owner; the Claude one has a preview channel. Pin and canary each, as now.
- Auth: if Claude subscription login fails through the SDK, task 9 may not reach most users.
- Without task 7, restarting a Claude pane loses the agent's memory, which terminal panes do not.
- Permission defaults differ per adapter (opencode allows by default). Each adapter needs a probe of what runs unasked.
- Relay: structured panes show nothing on a phone, so moving everything to ACP makes phones show less than they do now.

## Decisions for the user

Answered 27 Oct 2026: 1 lose the TUI (yes, no terminal option); 2 no Node, Go adapters in the binary; 3 no third-party adapters; 4 agent senders use the MCP `send_message` tool, delivered as a prefixed prompt; 5 relay still open; 6 answered in part: the Claude spike ran on a Max subscription login and it worked; API-key login is untested.

1. Lose the native Claude TUI for structured Claude panes, keep a terminal option indefinitely, or deprecate it later?
2. Accept Node 20+ for Claude, Codex and Gemini, installed by the user or by Flockdeck?
3. Accept pinned third-party adapters for the main agents, with a canary?
4. Keep mail as a user-turn prompt with prefix and caps, or require an MCP tool call for agent senders?
5. Lift the relay refusal for structured data (needs sealed transport), or accept phones as roster-only?
6. If subscription login does not work through the SDK, ship Claude structured as API-key only, or wait?
