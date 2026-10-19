---
type: CLI Command
title: flockdeck keys
description: Stores, clears and checks the API keys that API agents use, and sets their endpoints.
resource: cli_keys.go
tags: [cli, keys]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_keys.go, cli_keys_echo_unix.go, cli_keys_echo_windows.go, cli_keys_echo_darwin.go, cli_keys_echo_linux.go, cli_keys_echo_other.go, cli_keys_tty_windows.go, cli_keys_tty_other.go, internal/creds/creds.go, internal/creds/store.go, internal/agent/available.go, internal/agent/config.go]
---
## Synopsis

`flockdeck keys [list|set <agent>|clear <agent>|check <agent>|endpoint <agent> [<url> | default]]`

## Subcommands

| Subcommand | Aliases | Effect |
|---|---|---|
| (none) | | Runs `list`, then prints the usage. |
| `list` | `ls` | For each agent that uses a key: whether one is set and where it came from, plus the endpoint it talks to (credentials in the URL redacted). Stored keys for agents no longer in the catalog are listed too. |
| `set <agent>` | | Reads the key from the first line of standard input and stores it via `creds.Set`. On a terminal it prompts on stderr with echo off. |
| `clear <agent>` | `rm` | Forgets the stored key. If the key comes from an environment variable it says so; it cannot clear that. |
| `check <agent>` | `test` | Asks the agent's endpoint whether it accepts the key a pane would use. |
| `endpoint <agent>` | `url` | Shows the address the agent talks to. |
| `endpoint <agent> <url>` | | Changes it. Must be `http://` or `https://` with a host and no user or password. |
| `endpoint <agent> default` | | Restores the vendor's address; refused for an agent of your own, which has no default. |

`-h`, `--help` and `help` print the usage.

## Behaviour

- The key is never an argument and never printed back. After `set`, on a terminal, the key is checked against the endpoint with a 10 second timeout. A refused key says so; an unreachable endpoint does not fail the store.
- If the agent's key variable is set in the environment, `set` warns that the variable wins over the stored key.
- Key lookup order is the agent's own environment variables, then the stored key, then `FLOCKDECK_API_KEY` (see `agent.KeyNames`). A pane receives a stored key in its environment ([chat](/cli/chat.md)).
- `endpoint` writes through `agent.SetBaseURL` next to the agent config file. Running panes keep their address until restarted.
- `set`, `clear`, `check` and `endpoint` for an agent that takes no key fail and list the ones that do.

## Where it runs

Any shell. It touches the credential store and agent config only; no running instance is needed. Errors print `flockdeck keys: <error>` and exit 1; an interrupted `set` exits 130.
