# ACP registry survey

Written 27 Oct 2026 for task T193, from documentation only. No agent was started and no session was paid for. The registry was read from `https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json` on that date: 43 agents, no extensions.

## What the registry says and does not say

The schema (`agent.schema.json` in the registry repo) requires `id`, `name`, `version`, `description` and `distribution`. Distribution is `binary` (per-platform archive, `cmd`, optional `args`, `env`, `sha256`), `npx` or `uvx` (package, `args`, `env`). There is no field for authentication, for whether the entry is native or a wrapper, or for a Node requirement. The registry page says it lists only agents that support ACP authentication, but the JSON does not say which method, so auth has to come from each agent's own documentation. The "Kind" column below is my reading of the id, description and command, not registry data.

Counts: 21 npx only, 18 binary only, 2 uvx only, 2 binary and npx. An npx entry needs Node on the user's machine. A uvx entry needs uv and Python.

## Every entry

| Id | Distribution | Command | Kind | License |
| --- | --- | --- | --- | --- |
| agoragentic-acp | npx | `agoragentic-mcp@1.3.0 --acp` | not a coding agent (marketplace MCP package) | MIT |
| amp-acp | binary | `amp-acp` | wrapper (third party, spawns the amp CLI) | Apache-2.0 |
| antigravity-acp | binary | `agy_acp_server.par` | separate ACP server binary | proprietary |
| auggie | npx | `@augmentcode/auggie@0.36.0 --acp` | native (own CLI) | proprietary |
| autohand | npx | `@autohandai/autohand-acp@0.2.1` | adapter package | Apache-2.0 |
| bob | npx | `@ibm/bob@2.0.9 acp` | native (own CLI) | proprietary |
| claude-acp | npx | `@agentclientprotocol/claude-agent-acp@0.97.0` | wrapper (Claude Agent SDK) | proprietary |
| cline | npx | `cline@3.0.74 --acp` | native (own CLI) | Apache-2.0 |
| codebuddy-code | npx | `@tencent-ai/codebuddy-code@2.177.0 --acp` | native (own CLI) | Proprietary |
| codex-acp | npx | `@agentclientprotocol/codex-acp@2.3.0` | wrapper (codex) | Apache-2.0 |
| cortex-code | binary | `coco-1.0.73+180523.e6179a031de9-darwin-arm64/cortex acp serve` | native (own CLI) | proprietary |
| corust-agent | binary | `corust-agent-acp` | separate ACP binary | GPL-3.0-or-later |
| crow-cli | binary | `crow-cli acp` | native (own CLI) | Apache-2.0 |
| cursor | binary | `dist-package/cursor-agent acp` | native (own CLI) | proprietary |
| deepagents | npx | `deepagents-acp@0.1.7` | framework (LangChain) | MIT |
| devin | binary | `bin/devin acp` | native (own CLI) | proprietary |
| dimcode | npx | `dimcode@0.5.21 acp` | native (own CLI) | proprietary |
| dirac | npx | `dirac-cli@0.5.24 --acp` | native (own CLI) | Apache-2.0 |
| factory-droid | npx | `droid@0.249.0 exec --output-format acp-daemon` | native (own CLI) | proprietary |
| fast-agent | uvx | `fast-agent-acp==0.10.1 -x` | framework (agents built on it) | Apache 2.0 |
| gemini | npx | `@google/gemini-cli@0.65.0 --acp` | native (own CLI) | Apache-2.0 |
| github-copilot-cli | npx | `@github/copilot@1.0.103 --acp` | native (own CLI) | proprietary |
| glm-acp-agent | npx | `glm-acp-agent@1.15.0` | wrapper (GLM API) | Apache-2.0 |
| goose | binary | `goose acp` | native (own CLI) | Apache-2.0 |
| grok-build | npx | `@xai-official/grok@1.0.71 agent stdio` | native (own CLI) | proprietary |
| harn | binary | `harn serve acp` | native (own CLI) | Apache-2.0 |
| junie | binary | `Applications/junie.app/Contents/MacOS/junie --acp=true` | native (own CLI) | proprietary |
| kilo | binary, npx | `kilo acp` | native (own CLI) | MIT |
| kimchi | binary | `bin/kimchi --mode acp` | native (own CLI) | Apache-2.0 |
| kimi | binary | `kimi acp` | native (own CLI) | MIT |
| minimax-code | npx | `@minimax-ai/code@0.2.7 acp` | native (own CLI) | MIT |
| minion-code | uvx | `minion-code@0.1.44 acp` | native (own CLI) | AGPL-3.0 |
| mistral-vibe | binary | `vibe-acp` | separate vibe-acp binary | Apache-2.0 |
| nova | npx | `@compass-ai/nova@1.1.60 acp` | native (own CLI) | proprietary |
| opencode | binary | `opencode acp` | native (own CLI) | MIT |
| opencode-v2 | binary | `opencode acp` | native (own CLI) | MIT |
| pi-acp | npx | `pi-acp@0.0.34` | wrapper (pi) | MIT |
| poolside | binary | `pool-darwin-arm64 acp` | native (own CLI) | proprietary |
| qoder | npx | `@qoder-ai/qodercli@0.2.14 --acp` | native (own CLI) | proprietary |
| qwen-code | npx | `@qwen-code/qwen-code@0.26.1 --acp --experimental-skills` | native (own CLI) | Apache-2.0 |
| sigit | binary, npx | `sigit` | native (own CLI) | Apache-2.0 |
| stakpak | binary | `stakpak acp` | native (own CLI) | Apache-2.0 |
| vtcode | binary | `vtcode acp` | native (own CLI) | MIT |

Notes on the table:

- Kind is inferred. "native (own CLI)" means the command is the agent's own program with an `acp` subcommand or `--acp` flag. I did not read each project's code.
- Auth is not in the JSON, so it is not a column. Auth the Flockdeck manifest already records: opencode needs none (free model), Gemini and Codex need their own login. The agents asked about are covered below.
- `antigravity-acp` has no repository link. `agoragentic-acp` is a marketplace package, not a coding agent. `corust-agent` is GPL-3.0 and `minion-code` is AGPL-3.0, which would matter only if Flockdeck redistributed them. It does not.
- Native entries distributed as a binary (no Node): cursor, cortex-code, crow-cli, devin, goose, harn, junie, kilo, kimchi, kimi, opencode, poolside, stakpak and vtcode. I did not check any of them beyond the registry text.

## How the registry fits Flockdeck's rule

Flockdeck does not install agents, so the registry's `archive` and `package` fields are not used to install anything. What an entry gives Flockdeck is the command and arguments that start the ACP server (`cursor-agent acp`, `cline --acp`) and the fact that a route exists. The user installs the agent, as with opencode and Gemini today. An npx entry means the user has Node, which the manifest already handles with the "Node required" state (`needsNode`). Flockdeck bundles no Node.

Every wrapper entry is a third-party adapter package, which the 27 Oct decision excludes. For Claude and Codex the existing plan covers that.

## Cursor

Registry entry `cursor`, version 2026.10.22, binary only, proprietary, website `https://cursor.com/docs/cli/acp`. It is the agent's own CLI, not a wrapper. The command is `./dist-package/cursor-agent acp` (Windows: `./dist-package\cursor-agent.cmd acp`) from a tar.gz or zip at `downloads.cursor.com`, with builds for darwin, linux and windows on x86_64 and aarch64. The registry gives no `sha256`.

Cursor's page says to start it with `agent acp`, so `agent` and `cursor-agent` look like two names for the same program (the page's Neovim example assumes `~/.local/bin/agent`). Transport is newline-delimited JSON-RPC over stdio, with logs on stderr. The flow is `initialize`, `authenticate`, `session/new` or `session/load`, `session/prompt`, `session/update`, `session/request_permission`, `session/cancel`.

Auth: the method id is `cursor_login`. The user can sign in before startup with `agent login`, or pass `--api-key` / `CURSOR_API_KEY`, or `--auth-token` / `CURSOR_AUTH_TOKEN`. This fits the existing `auth: external` entry shape: Flockdeck never logs in, and `CURSOR_API_KEY` would have to be named in `passEnv`.

Capabilities: modes `agent`, `plan` and `ask`; permission replies `allow-once`, `allow-always`, `reject-once`; MCP servers read from `.cursor/mcp.json` (team servers set in the dashboard are not supported over ACP); `session/load` for resume. Cursor's sample client advertises `fs` and `terminal` as false, which is what `internal/acp` does too (`Initialize` in `calls.go`), so Cursor does not need those from the client.

Does the client in `internal/acp` on origin/main (4efb7311) work with it? The base path does: initialize, authenticate, session/new, session/prompt, permission requests and the update stream are all implemented. Two gaps:

1. Cursor sends two extension requests that block the agent until the client answers: `cursor/ask_question` and `cursor/create_plan`. `serve` in `internal/acp/calls.go` answers every request except `session/request_permission` with JSON-RPC error -32601. What Cursor does with that error is not documented. It may skip the question, fail the turn or hang. The page itself warns that blocking extension methods stall the agent until answered. One real session would settle it.
2. `cursor/update_todos`, `cursor/task` and `cursor/generate_image` are notifications. Flockdeck can ignore them, but then plan and todo views have nothing to show, which counts against the feature-parity rule.

`session/load` is also not implemented in the client, so resume is not available for Cursor or any other agent yet.

Install: Cursor's ACP page has no install instructions. The registry points at a download archive, and I did not read Cursor's installer docs, so the supported user install route is unverified, as is whether `cursor-agent.cmd` on Windows needs a separate Node. Check both before writing a manifest `install` text.

Verdict: offer, once `cursor/ask_question` and `cursor/create_plan` are answered by the client. Unverified: the Windows install and the behaviour on -32601.

## Aider

No registry entry, and none of the 43 ids or descriptions mention it. A search found no official Aider ACP support. A comparison table on a third-party site lists Aider as "in progress", which I could not confirm. AiderDesk (a separate desktop project) has an ACP bridge, but it is a stdio bridge to a running AiderDesk server, so it is another product. Aider's own CLI exits after one `--message`, so there is no native route, and an own adapter would need a long-lived Python process or Aider's Python API.

Verdict: drop. No real ACP route. An own adapter is possible, but the plan already rated it low value.

## GitHub Copilot CLI

Registry entry `github-copilot-cli`, version 1.0.103, npx `@github/copilot@1.0.103 --acp`, proprietary. It is the agent's own CLI. GitHub's docs describe `copilot --acp` as ACP server mode, in public preview and "subject to change". It speaks stdio (newline-delimited JSON) by default, or TCP on 127.0.0.1 with `--port`. The client sets cwd and MCP servers in `session/new`. Server-wide flags: `--available-tools`, `--excluded-tools`, `--effort`, `--sandbox` / `--no-sandbox`. Slash commands are sent as prompts and advertised through `available_commands_update`; `/diff`, `/resume`, `/login` and `/settings` do not work over ACP.

Auth: a GitHub login, or a BYOK provider through `COPILOT_PROVIDER_*` variables or `COPILOT_PROVIDERS_CONFIG`; with BYOK no GitHub login is needed. The docs page does not mention `GH_TOKEN`.

Node: the registry route is npx, so Node. The docs page does not list other install methods (Homebrew, winget, a standalone binary), so whether a Node-free install exists is unverified.

Verdict: offer, marked preview. Needs Node unless a standalone install is confirmed. I did not check what Copilot sends beyond standard ACP.

## Amp

Registry entry `amp-acp`, version 0.10.0, binary (darwin and linux on aarch64 and x86_64, windows x86_64 only), Apache-2.0, with `sha256` per archive. It is a wrapper by an individual (`tao12345666333`, 101 stars, 130 commits), not by Sourcegraph; its README does not mention Sourcegraph. It spawns the installed `amp` CLI through its streaming JSON interface (an `@ampcode/sdk` fallback exists), so Amp itself must be installed and signed in (`amp login`, or `AMP_API_KEY`). The README says the binary has no JavaScript runtime dependency; the npx route needs Node 18 or newer. The source looks like TypeScript, so the binary is probably a compiled bundle.

There is no `amp` entry in the registry, only this wrapper, so Amp has no ACP mode of its own that I could find.

Verdict: drop the registry route. It is a third-party adapter, which the 27 Oct decision rules out. Amp could be reached by an own Go adapter over the CLI's streaming JSON. That is a separate task, and I did not check that interface's stability.

## Cline

Registry entry `cline`, version 3.0.74, npx `cline@3.0.74 --acp`, Apache-2.0. Cline's docs say the CLI speaks ACP and a client launches `cline --acp`, so it looks native, though the page does not say so outright. Install is `npm i -g cline` (a client that does not inherit the shell `PATH` should use the absolute path from `which cline`). The page lists no Node version.

Auth: optional at setup. Clients can prompt for sign-in (Cline, ClinePass or a ChatGPT subscription), credentials from `cline auth` are reused, and `CLINE_API_KEY` skips sign-in. Other variables: `CLINE_PROVIDER`, `CLINE_MODEL`. Capabilities listed: Plan/Act modes, model and provider selection, permission prompts for edits and commands (nothing auto-approved unless `--auto-approve true`), session resume for clients with session loading, image prompts, personal or organisation billing.

Verdict: offer, Node required (same class as Gemini). The sign-in methods arrive as `authMethods` and the existing `AuthRequiredError` path shows them. Flockdeck never logs in, so the user runs `cline auth` first.

## Verdicts

| Agent | Verdict | Reason |
| --- | --- | --- |
| Cursor | offer, after two fixes | Native `cursor-agent acp`, binary. Needs `cursor/ask_question` and `cursor/create_plan` answered; install route unverified. |
| Copilot CLI | offer (preview) | Native `--acp`. Node unless a standalone install exists. |
| Cline | offer | Native `--acp`. Needs Node. |
| Amp | drop | Only a third-party wrapper. An own Go adapter is a separate decision. |
| Aider | drop | No ACP route anywhere. |

## Not checked

- No agent was run. Nothing here shows what any of them does at `initialize`, which `authMethods` they list, or how they answer -32601.
- "Kind" for the other entries is inferred from the registry text.
- The registry changes often (Cursor's archive URL is dated 2026.10.22). Re-fetch before relying on a version.
