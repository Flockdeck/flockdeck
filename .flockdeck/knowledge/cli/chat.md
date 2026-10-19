---
type: CLI Command
title: flockdeck chat
description: Flockdeck's own chat client, which a pane running an API agent starts.
resource: cli_chat.go
tags: [cli, pane, chat]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_chat.go, internal/chat/flags.go, internal/chat/session.go]
---
## Synopsis

`flockdeck chat [flags] [--] [task]`

Started by a pane, not by a person. It is the only subcommand that skips `useConsole()` in `main`, so that a chat run from a detached instance does not write to the terminal that launched it.

## Flags (defined in `internal/chat/flags.go`)

| Flag | Default | Effect |
|---|---|---|
| `-agent` | `FLOCKDECK_AGENT` | Agent id the pane was started as. |
| `-model` | `FLOCKDECK_MODEL` | Model to ask for. |
| `-session` | `FLOCKDECK_PANE` | Conversation id, which is also the pane id. |
| `-resume` | false | Carry on the conversation this session id already has. |
| `-wire` | `FLOCKDECK_WIRE`, else the catalog entry | Request shape: anthropic, openai or gemini. |
| `-base-url` | `FLOCKDECK_BASE_URL`, else the catalog entry | Endpoint root. |
| `-key-env` | `FLOCKDECK_KEY_ENV`, else the catalog entry | Comma-separated names an API key may arrive in. |
| `-max-tokens` | 0 | Ceiling on one answer; 0 leaves it to the model. |
| `-cwd` | working directory | Directory the chat's tools are confined to. |

Remaining arguments are the opening task. The pane's callback address and token come from [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md). Each `FLOCKDECK_*` name also accepts the older `PERCH_*` name, with a warning.

## Behaviour in the root package

- Fills wire, base URL, key variables and default model from the agent's catalog entry, `agents.json` included, wherever flag and environment left them empty.
- Gives the model file, directory and command tools confined to the working directory. If that directory cannot be resolved, it runs with no tools.
- Hides from commands the model runs any environment variable that holds a key stored by [keys](/cli/keys.md) set.
- Reads the stored key and current endpoint at run time, so changes from `keys` apply to a running pane.

Errors print `flockdeck chat: <error>` and exit 1. `-h` exits 0.
