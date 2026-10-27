---
type: CLI Command
title: flockdeck agents
description: Prints the catalog of agents Flockdeck can run, with models, install state and roles.
resource: main.go
tags: [cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go, internal/agent/catalog.go, internal/agent/available.go, internal/agent/config.go, internal/agent/roles.go]
---
## Synopsis

`flockdeck agents`

Takes no flags; `printAgents` runs without parsing any. Extra arguments are ignored. It exits 0.

## Output

- A note when the user's `agents.json` could not be read.
- One block per agent that is not hidden: `<id> - <name>  [<state>[, default]]`, a `models:` line, and for an agent that is not available an `install:` line with the entry's install hint.
- A closing line pointing at `flockdeck -agent <id>` and `flockdeck spawn --agent <id> --model <model> <task>` ([spawn](/cli/spawn.md)).
- Then the roles a helper can be started as (`printRoles`), when any exist.

## Where it runs

Any shell. It reads the built-in catalog plus the user's `agents.json`, and checks whether each agent's program is on the machine ([agent package](/packages/agent.md)). It needs no running instance. The ids it prints are what [flockdeck](/cli/flockdeck.md) `-agent`, [spawn](/cli/spawn.md) `-agent` and `-role`, and [keys](/cli/keys.md) accept.
