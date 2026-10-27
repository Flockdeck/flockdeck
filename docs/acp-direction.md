# ACP direction

Decided 27 Oct 2026 by the project owner. This page says what Flockdeck is being built toward, what exists, and what not to build on. Where this page and an older page disagree about agents, mail, `flockdeck chat` or the task board, this page is the current direction. Nothing listed as planned for removal has been removed yet; the older pages still describe the code as it is.

## Decisions

1. Flockdeck runs every agent over the Agent Client Protocol (ACP). There are no agent TUIs and no terminal-pane fallback.
2. An agent with no native ACP gets an adapter written by Flockdeck in Go and built into the `flockdeck` binary as a subcommand. No Node and no third-party adapter packages. Agents with native ACP run directly: opencode, Gemini CLI, Cursor, Copilot CLI, Cline. Agents with no ACP route are dropped: Aider, Amp. Flockdeck does not install agents and does not tell the user about an agent's own dependencies. If an agent will not start, the pane says the agent is unavailable.
3. Agent-to-agent mail goes through Flockdeck. Each agent gets a Flockdeck MCP server with `send_message` and `list_peers`. Flockdeck delivers the message to the receiver as a `session/prompt` with a fixed sender prefix, and the turn's stop reason is the delivery acknowledgement. The shell inbox (`flockdeck msg`, `flockdeck inbox`), hook notices and typed wake lines are to be retired. A small audit log of messages stays.
4. `flockdeck chat` is to be removed. API keys stay in Settings and are passed to the agent's environment.
5. The task board is to be removed once the new mail path works.
6. Before ACP becomes the default, the structured pane must reach parity with what the TUIs gave the user: slash menu, model and mode switching, resume, images, `@file`, queue, shell escape, rewind, export and the rest of [the parity matrix](acp-parity-matrix.md).

## Status

Built, on origin/main:

- `internal/acp` client (initialize, authenticate, session/new, session/prompt, session/cancel, permission requests, update stream) and `internal/structured` runtime with a 16-deep prompt queue.
- A beta opencode entry; hidden Gemini and Codex entries that use third-party npm adapters. These are the pieces decision 2 replaces for Codex.
- The adapter manifest, the Node check and the weekly canary ([acp-adapters.md](acp-adapters.md)).
- Message delivery states in `internal/team`. A message to a terminal pane has queued, notified, woken and read, and the text wake; these belong to the mail path decision 3 retires. A message to a structured pane is a `session/prompt` with its own states: queued, delivered, handled, undeliverable, with the stop reason stored and a retry cap ([structured pane](structured-pane.md)).

Built: the Flockdeck MCP server with `send_message` and `list_peers` (T186, [Flockdeck MCP server](flockdeck-mcp-server.md)). It records a message in the receiver's inbox as before; delivery to the receiver as a prompt is T187.

Built, hidden: the Go adapter for Claude Code (T204, [Claude Code adapter](acp-claude-adapter.md)) and the Go adapter for Codex (T206, [Codex adapter](acp-codex-adapter.md); built from the route document and a fake app-server, never run against a real codex). In progress: the capability spike per agent (T208).

Researched, not built:

- [Claude headless spike](claude-headless-spike.md): `claude -p --input-format stream-json --output-format stream-json` supports permissions, interrupt, mid-turn messages, resume, images, MCP servers and slash commands, so a Go adapter needs no Node. The control protocol is thinly documented, and resume cannot replay history without reading Claude's session files.
- [Codex route](codex-adapter-route.md): `codex app-server --listen stdio://` covers what ACP needs. The adapter built from it is listed above; the route itself was not run against a real binary.
- [Registry survey](acp-registry-survey.md): Cursor, Copilot CLI and Cline are native. Amp has only a third-party wrapper and Aider has nothing. Its "Node required" notes for Copilot and Cline no longer apply under decision 2. Cursor needs its `cursor/ask_question` and `cursor/create_plan` requests answered.
- [Parity matrix](acp-parity-matrix.md): 10 largest gaps between the structured pane and the TUIs.
- [Plan](acp-plan.md): the original T182 plan, updated for these decisions.

Planned, by board task:

| Area | Tasks |
| --- | --- |
| Adapters | T204 Claude (built, hidden), T206 Codex, T207 generic "agent unavailable" state in place of the Node check, T191 Claude manifest entry and canary, T192 Gemini and Codex end to end |
| Mail | T185 fill `mcpServers`, T186 Flockdeck MCP server, T187 delivery states and audit log, T188 mail in the conversation view, T199 messaging parity, T196 retire typed wake |
| Removals | T201 remove `flockdeck chat`, T203 remove the task board, T202 API keys in Settings |
| Switch to default | T189 session/load and resume, T195 default to structured |
| Parity | T208 spike (in progress), T209 bridge reads all session updates, T210 slash menu, T211 model, effort and mode, T212 composer, T213 queue, T214 shell escape and background tasks, T215 rewind and fork, T216 export and usage, T217 config surfaces |

T184 (decision doc) is covered by this page. T67 is the review of ACP-1.

## Open

- Phones. Structured data is refused over the relay, so a phone shows less once every agent is structured. No decision yet.
- The plain Shell pane and user-written `agents.json` CLI entries. Decision 1 says no terminal fallback; whether a bare shell pane stays is not decided.
- Claude resume. `session/load` needs history replay, and Claude's resume sends none. Reading its session files is the only route found and is not a stable format.
- Claude's stdio control protocol is found by experiment, not documented. It needs a pinned version range and CI probes.
- Codex: how Flockdeck finds a pinned Codex binary without Node, whether the app-server accepts messages carrying `"jsonrpc":"2.0"`, Windows sandbox setup, and resume replay.
- Cursor: install route on Windows and what it does with error -32601 on its two blocking requests.
- Which unstable ACP methods Flockdeck depends on (parity matrix, section 9).
- Existing users: saved layouts and agents with terminal panes. Migration is not planned yet.

## Do not build on these

They are planned for removal. Existing code keeps working until its removal task lands, but new work should not depend on it, extend it or document it as the way agents work.

- `flockdeck msg`, `flockdeck inbox` (including `-follow` and `-wait`), hook notices, the typed wake line, `agent.WakeRule` and `flockdeck wake`. Mail goes through the MCP tools and `session/prompt`.
- `flockdeck chat`, `runner: "api"` agents and the chat transcript.
- The task board (`flockdeck task`, `tasks.json`).
- Agent TUIs in terminal panes, screen-reading rules, and status taken from Claude Code hooks. New agents are ACP only.
- Third-party ACP adapters (`@agentclientprotocol/claude-agent-acp`, `@agentclientprotocol/codex-acp`), anything that needs Node, and the "Node required" state.
- New terminal entries for Aider or Amp.

What to build on: the `internal/acp` client, the structured runtime, the bridge, the Go adapters for Claude and Codex, and the Flockdeck MCP server.
