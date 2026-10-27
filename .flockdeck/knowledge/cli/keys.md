---
type: CLI Command
title: flockdeck keys
description: Stores, clears, lists and checks the API keys saved for agents that take one.
resource: cli_keys.go
tags: [cli, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_keys.go, cli_keys_echo_unix.go, cli_keys_echo_windows.go, cli_keys_echo_darwin.go, cli_keys_echo_linux.go, cli_keys_echo_other.go, cli_keys_tty_windows.go, cli_keys_tty_other.go, internal/creds/creds.go, internal/creds/store.go, internal/apikey/apikey.go, internal/apikey/check.go, internal/agent/available.go]
---
## Synopsis

`flockdeck keys [list|set <agent>|clear <agent>|check <agent>]`

## Current state

The agents that take a key are the built-in structured ones with an `api` block (`gemini-acp`, `codex-acp`, `claude-acp`); an agents.json entry with an `api` block is refused. `set` and `check` refuse every other id and name the agents that take a key. `clear <id>` removes a stored key for any id. A stored key is set in the agent's environment when its pane starts (T202).

## Subcommands

| Subcommand | Aliases | Effect |
|---|---|---|
| (none) | | Runs `list`, then prints the usage. |
| `list` | `ls` | For each agent that takes a key: whether one is set and where it came from, plus the endpoint it talks to when it has one (credentials in the URL redacted). Stored keys for ids no longer in the catalog are listed too. Prints "no agents that use a key" when there is nothing to show. |
| `set <agent>` | | Reads the key from the first line of standard input and stores it via `creds.Set`. On a terminal it prompts on stderr with echo off. |
| `clear <agent>` | `rm` | Forgets the stored key. If the key comes from an environment variable it says so; it cannot clear that. |
| `check <agent>` | `test` | Asks the agent's endpoint whether it accepts the key that `apikey.KeyFor` finds, and says where that key comes from. |

`-h`, `--help` and `help` print the usage.

## Behaviour

- The key is never an argument and never printed back. After `set`, on a terminal, the key is checked with `apikey.CheckKey` against the endpoint with a 10 second timeout. A refused key says so; an unreachable endpoint does not fail the store.
- If the agent's key variable is set in the environment, `set` warns that the variable wins over the stored key.
- For a structured agent the key is the first of its key variables that its `passEnv` lets through, else the stored key (`creds.Resolve`). The vendor's usual variable and `FLOCKDECK_API_KEY` are not read for it. `check` asks about that key.
- `set` and `check` for an id that takes no key fail and list the ones that do.

## Where it runs

Any shell. It touches the credential store and agent config only; no running instance is needed. Errors print `flockdeck keys: <error>` and exit 1; an interrupted `set` exits 130.
