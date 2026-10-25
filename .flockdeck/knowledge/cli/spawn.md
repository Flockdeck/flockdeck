---
type: CLI Command
title: flockdeck spawn
description: Starts another agent or a shell from inside a pane, optionally in a worktree, a named tab or from a baton.
resource: main.go
tags: [cli, pane]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go, internal/hooks/hooks.go, internal/workspace/spawn.go]
---
## Synopsis

`flockdeck spawn [flags] <task>`

Flags may come before or after the task: `orderSpawnArgs` moves them in front. Everything after a bare `--` is task text.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-worktree <branch>` | see cli help | Branch for a git worktree made for the helper. |
| `-name <label>` | none | What to call the helper for its work, in 2-5 words (the PR or topic). One line of at most 40 characters; the window and other agents show it in place of the directory name. The helper can change it with [pane-name](/cli/pane-name.md). |
| `-no-worktree` | false | Start the helper in the caller's own checkout. Cannot go with `-worktree`. |
| `-split` | false | Place the helper beside this pane, not in a new tab. |
| `-tab <title>` | none | Put the helper in the tab of that name in this project, made if missing. Takes precedence over `-split`. |
| `-shell` | false | Start a shell instead of an agent. |
| `-record` | false | Record the helper's interaction; only works once recording is on in the window. |
| `-agent <id>` | none | Agent to start; see [agents](/cli/agents.md). |
| `-model <model>` | none | One of that agent's models. |
| `-role <id>` | none | Role to start as. Its agent and model apply where `-agent` and `-model` are unset. |
| `-baton <ref>` | none | Start from a handoff: `self`, a pane id, a baton id ([baton](/cli/baton.md)) or a notes file. An existing file is sent as `path:<absolute path>`. |
| `-task <id>` | none | Start the helper for a task of the project board: the task is the opening prompt when no text is given, and it is assigned to the helper and its team. |
| `-baton-send-elsewhere` | false | Ask the user to allow the baton to go to an agent of a different or unknown company. Needs `-baton`. |

## Where it runs

Inside a pane. It needs [FLOCKDECK_API](/config/flockdeck-api.md) and [FLOCKDECK_TOKEN](/config/flockdeck-token.md); [FLOCKDECK_PANE](/config/flockdeck-pane.md) is passed along if set. Without the first two it fails with "this only works inside a flockdeck pane".

## Validation

A task is required unless `-shell` or `-baton` is given. `-shell` cannot be combined with `-baton`, `-agent`, `-model`, `-record` or `-role`. Agent, model and role are checked against the catalog before anything is sent. An agent that is not installed prints a warning.

## Output and errors

Sends the request through `hooks.Spawn` to the loopback API ([spawn endpoint](/api/hooks-spawn.md)), waiting up to a minute. On success it prints `started agent <pane-id> [as <role>] [in <cwd>]`, and for a baton `from baton <id> kept at <path>`. A timeout is reported as the instance probably being busy. `-h` exits 0; other errors exit 1.
