# ACP direction

Decided 27 Oct 2026 by the project owner. This page says what Flockdeck is being built toward, what exists, and what not to build on. Where this page and an older page disagree about agents, mail, `flockdeck chat` or the task board, this page is the current direction. Nothing listed as planned for removal has been removed yet; the older pages still describe the code as it is. Decision 1 was changed on 6 Nov 2026; see below.

## Decisions

1. Flockdeck runs agents over the Agent Client Protocol (ACP) wherever there is a route, and a new Claude Code or opencode pane starts structured by default. A terminal pane stays as the opt-out. (Changed 6 Nov 2026. This decision used to say there are no agent TUIs and no terminal-pane fallback.)
2. An agent with no native ACP gets an adapter written by Flockdeck in Go and built into the `flockdeck` binary as a subcommand. No Node and no third-party adapter packages. Agents with native ACP run directly: opencode, Gemini CLI, Cursor, Copilot CLI, Cline. Agents with no ACP route are dropped: Aider, Amp. Flockdeck does not install agents and does not tell the user about an agent's own dependencies. If an agent will not start, the pane says the agent is unavailable.
3. Agent-to-agent mail goes through Flockdeck. Each agent gets a Flockdeck MCP server with `send_message` and `list_peers`. Flockdeck delivers the message to the receiver as a `session/prompt` with a fixed sender prefix, and the turn's stop reason is the delivery acknowledgement. The shell inbox (`flockdeck msg`, `flockdeck inbox`), hook notices and typed wake lines are to be retired. A small audit log of messages stays.
4. `flockdeck chat` is to be removed. API keys stay in Settings and are passed to the agent's environment.
5. The task board is to be removed once the new mail path works.
6. Before ACP becomes the default, the structured pane must reach parity with what the TUIs gave the user: slash menu, model and mode switching, resume, images, `@file`, queue, shell escape, rewind, export and the rest of [the parity matrix](acp-parity-matrix.md).

## Status

Changed 6 Nov 2026 (T195): structured is the default for new Claude Code and opencode panes, and the terminal entries stay. Three reasons, in the order they mattered:

- A person whose structured pane will not start, or needs a sign-in Flockdeck cannot finish, would otherwise have no way to use the agent. The terminal entry, Settings, General, "New panes start as", `flockdeck spawn -terminal` and the "Restart as terminal" button on a failed pane are the way back.
- Some agents have no ACP route and cannot move: Cursor Agent, Aider, plain shells, and any CLI a user adds in `agents.json`. Their panes are terminals, so the terminal code (screen reading, typed wake, the shell inbox) keeps running for them.
- A phone cannot yet answer a structured pane's permission request. It is told "Answer on the desktop". Installs with paired devices or remote access keep terminal as the default for new panes until the phone app and the relay path ship.

Codex and Gemini CLI stay terminal by default. Their structured entries are hidden until T192 has run them against the real programs. The default applies to new panes only: a saved layout restores each pane as it was. How the setting works is in the [structured pane](structured-pane.md) page and the help page "Agents and models".

Built, on origin/main:

- `internal/acp` client (initialize, authenticate, session/new, session/prompt, session/cancel, permission requests, update stream) and `internal/structured` runtime with a 16-deep prompt queue.
- The opencode entry, now the structured default for new opencode panes; hidden Gemini and Codex entries that use third-party npm adapters. These are the pieces decision 2 replaces for Codex.
- The adapter manifest, the Node check and the weekly canary ([acp-adapters.md](acp-adapters.md)).
- Message delivery states in `internal/team`. A message to a terminal pane has queued, notified, woken and read, and the text wake; these belong to the mail path decision 3 retires. A message to a structured pane is a `session/prompt` with its own states: queued, delivered, handled, undeliverable, with the stop reason stored and a retry cap ([structured pane](structured-pane.md)).

Built: the Flockdeck MCP server with `send_message` and `list_peers` (T186, [Flockdeck MCP server](flockdeck-mcp-server.md)). It records a message in the receiver's inbox as before; delivery to the receiver as a prompt is T187. Mail shows in the conversation of a structured pane as `message` entries, received and sent, with their delivery state (T188, API 1.14, [structured pane](structured-pane.md)).

Built (T202): per-agent API keys. A key stored in Settings or with `flockdeck keys set` is set in the adapter's environment under the one variable the catalog names for that agent (Gemini CLI `GEMINI_API_KEY`, Codex `OPENAI_API_KEY`) and reaches no other process, recording or message. A key is never required. The Claude adapter has a catalog entry only when T191 lands; it takes `ANTHROPIC_API_KEY` the same way ([adapters](acp-adapters.md)).

Built (T189): saved session id per structured pane, `session/load` with replay handling and `session/resume` in the client and runtime, fallback to a new session with a fixed notice ([structured pane](structured-pane.md)). Claude's adapter resumes; Codex's already did; neither loads.

Built: the Go adapter for Claude Code (T204, [Claude Code adapter](acp-claude-adapter.md)), listed and the default for new Claude Code panes since 6 Nov 2026, and the Go adapter for Codex (T206, hidden, [Codex adapter](acp-codex-adapter.md); built from the route document and a fake app-server, never run against a real codex). In progress: the capability spike per agent (T208).

Built (T214, API 1.20): the `!` shell escape of a structured pane, run by `internal/shellrun` and ended with the pane. Flockdeck does not advertise the ACP `terminal` capability; see the [parity matrix](acp-parity-matrix.md) for why background tasks the agent starts are not listed.

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
| Mail | T185 fill `mcpServers`, T186 Flockdeck MCP server, T187 delivery states and audit log, T199 messaging parity, T196 retire typed wake |
| Removals | T201 remove `flockdeck chat`, T203 remove the task board, T202 API keys in Settings |
| Switch to default | T189 session/load and resume, T195 default to structured |
| Parity | T208 spike (in progress), T209 bridge reads all session updates, T210 slash menu, T211 model, effort and mode, T212 composer, T213 queue, T214 shell escape and background tasks, T215 rewind and fork, T216 export and usage, T217 config surfaces |

T184 (decision doc) is covered by this page. T67 is the review of ACP-1.

## Open

- Phones. Structured data is refused over the relay. Decided 6 Nov 2026: a waiting structured pane sends a paired phone "Answer on the desktop", and the phone cannot answer until the phone app (T6) and the relay path (T7) ship. Until then installs with paired devices keep terminal as the default.
- The plain Shell pane and user-written `agents.json` CLI entries. A bare shell pane and these entries stay terminals; they have no ACP route.
- Resume gaps found in review of T189, left as they are: replay marking needs the update's `sessionId` to equal the id sent to `session/load`, so an agent that leaves it out of replay updates has them taken as live (agent text shows as replies and the user turns are lost; it fails soft and has no security effect). The Claude adapter's "already open" check is per adapter process, so two panes with the same saved id run two `claude --resume` on one conversation.
- Claude `session/load`: done (T230). The adapter replays claude's stored session file; the format is not documented by claude, so the reader skips what it does not know (`docs/acp-claude-adapter.md`). Codex still has none.
- Claude's stdio control protocol is found by experiment, not documented. It needs a pinned version range and CI probes.
- Codex: how Flockdeck finds a pinned Codex binary without Node, whether the app-server accepts messages carrying `"jsonrpc":"2.0"`, Windows sandbox setup, and resume replay.
- Cursor: install route on Windows and what it does with error -32601 on its two blocking requests.
- Which unstable ACP methods Flockdeck depends on (parity matrix, section 9).
- Existing users: saved layouts restore each pane as it was, so a terminal pane stays a terminal pane. The default applies to new panes only. Whether the terminal entries get an end date is for after one release.

## Do not build on these

They are planned for removal. Existing code keeps working until its removal task lands, but new work should not depend on it, extend it or document it as the way agents work. Items that terminal panes still need are marked: terminal panes stay as the opt-out, so those routes are not removed until it is decided whether the terminal entries get an end date.

- `flockdeck msg`, `flockdeck inbox` (including `-follow` and `-wait`), hook notices, the typed wake line, `agent.WakeRule` and `flockdeck wake`. Mail goes through the MCP tools and `session/prompt`.
- `flockdeck chat`, `runner: "api"` agents and the chat transcript.
- The task board (`flockdeck task`, `tasks.json`).
- Agent TUIs in terminal panes, screen-reading rules, and status taken from Claude Code hooks, as the way a new agent is supported. A terminal pane stays for the opt-out and for agents with no ACP route. New agents with an ACP route are ACP only.
- Third-party ACP adapters (`@agentclientprotocol/claude-agent-acp`, `@agentclientprotocol/codex-acp`), anything that needs Node, and the "Node required" state (the Node check, `needsNode`, the install lines and the third-party Codex entry are gone; an agent that cannot start is only "unavailable", T207).
- New terminal entries for Aider or Amp.

What to build on: the `internal/acp` client, the structured runtime, the bridge, the Go adapters for Claude and Codex, and the Flockdeck MCP server.
