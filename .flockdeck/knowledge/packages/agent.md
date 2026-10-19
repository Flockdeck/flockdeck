---
type: Package
title: agent
description: Catalog of the coding agents, roles and routing policy Flockdeck can start in a pane.
resource: internal/agent
tags: [agent, catalog, roles, routing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/agent]
---
# internal/agent

Describes what a pane can run. A pane is a shell or an agent; an agent is a `Spec` (a program to run, or an API to talk to) plus a model. Every agent-specific decision is a field on the spec, so a new agent is a table entry.

## Main types and functions

| Name | Role |
|---|---|
| `Spec`, `Model`, `Caps`, `Arg`, `APISpec`, `Patterns` | One agent; see [agent spec](/data/agent-spec.md) |
| `Builtins()` | Ten built-ins: `claude`, `codex`, `gemini`, `aider`, `opencode`, `cursor-agent` (CLI runners), then API runners `anthropic`, `openai`, `google`, `openai-compatible` |
| `Catalog`, `Load()`, `LoadFrom(dir)`, `Merge(f)` | Built-ins with the user's [agents.json](/data/agents-json.md) merged over them. Never fails; problems go in `Catalog.Notice` |
| `BuildArgv(spec, resume, tokens)` | Expands `{{session}}`, `{{model}}`, `{{settings}}`, `{{prompt}}`, `{{cwd}}`, `{{pane}}` into an argv in one pass |
| `Role`, `BuiltinRoles()`, `Catalog.MayStart`, `Catalog.Choose` | Roles: [agent role](/data/agent-role.md) |
| `RoutingPolicy`, `Catalog.RoutingFor`, `SetRouting` | Routing settings, consumed by [route](/packages/route.md); see [routing policy](/data/routing-policy.md) |
| `Available`, `Refresh`, `KeyNames`, `OwnKeyEnv`, `VendorsOwn` | Whether an agent can start now (PATH lookup or key present), and API key lookup order |
| `SetDefaults`, `SetBaseURL`, `CheckBaseURL` | Atomic edits to agents.json |

## Touches

- Reads and writes `agents.json` in the state dir (through [store](/packages/store.md)); reads `keys.json` only to learn whether a key is set.
- Reads the environment variables named by an API spec's `keyEnv`, plus `FLOCKDECK_API_KEY` ([env](/config/flockdeck-api-key.md)).
- `Available` does a PATH lookup, cached for 5 seconds.

## Depended on by

[chat](/packages/chat.md) (key rules), [route](/packages/route.md), and the workspace and session code that builds pane commands.

## Notes

- Only the `claude` entry claims hooks, resume, transcript and trust. Other CLI agents claim at most a briefing in front of the opening task (`ContextPrompt`). API runners use hooks, resume, transcript and hook-style context.
- See [agents.json write rules](/gotchas/agents-json-write-rules.md), [prompt token expansion](/gotchas/prompt-token-expansion.md) and [Claude env stripping](/gotchas/claude-env-stripping.md).
