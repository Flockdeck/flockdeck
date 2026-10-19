---
type: CLI Command
title: flockdeck
description: The root command: opens the workspace and window, or dispatches to a subcommand.
resource: main.go
tags: [cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go, cli_update.go, console_unix.go, console_windows.go, console_other.go, fail_other.go, fail_windows.go, internal/server/attach.go, internal/store/store.go]
---
## Synopsis

`flockdeck [flags]` starts Flockdeck, or attaches to an instance that is already running. `flockdeck <subcommand> ...` runs one of the subcommands below. Flags are defined in `flockdeckFlagSet`. Most can be seeded from an environment variable; a flag given on the command line wins.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-C <directory>` | [FLOCKDECK_DIR](/config/flockdeck-dir.md), else "." | Directory to open the workspace on. Setting the variable alone counts as naming a directory. |
| `-agent <id>` | [FLOCKDECK_START_AGENT](/config/flockdeck-start-agent.md) | Agent new panes start as for this run. Checked only for a run that starts one, not for `-quit`. |
| `-new` | false ([FLOCKDECK_FRESH](/config/flockdeck-fresh.md)) | Start with one pane instead of the saved layout, which this run's layout then replaces. |
| `-shell` | false ([FLOCKDECK_SHELL_FIRST](/config/flockdeck-shell-first.md)) | First pane is a shell, not an agent. |
| `-no-window` | false ([FLOCKDECK_NO_WINDOW](/config/flockdeck-no-window.md)) | Open no window; print the URL and keep serving. |
| `-detach` | false ([FLOCKDECK_DETACH](/config/flockdeck-detach.md)) | Keep running without a window; run it again to reattach. |
| `-quit` | false | Stop a running instance and its agents. |
| `-solo` | false ([FLOCKDECK_SOLO](/config/flockdeck-solo.md)) | Always start a new instance instead of attaching. |
| `-version` | false | Print `flockdeck <version> (<os>/<arch>)` and exit 0. |

## Dispatch order

`main` checks `os.Args[1]` in this order: `help`, `hook`, `statusline`, [spawn](/cli/spawn.md), [baton](/cli/baton.md), [peer-name](/cli/peer-name.md), [close](/cli/close.md), [team](/cli/team.md), [msg](/cli/msg.md), [inbox](/cli/inbox.md), [task](/cli/task.md), [recordings](/cli/recordings.md), [agents](/cli/agents.md), [chat](/cli/chat.md), [keys](/cli/keys.md), [remote](/cli/remote.md), [update](/cli/update.md), [helpers](/cli/helpers.md). The first match runs and returns. [hook](/cli/hook.md) and [statusline](/cli/statusline.md) are hidden: panes run them, not people. Anything else falls through to the flags above.

## help

`flockdeck help` prints the top-level usage and exits 0. `flockdeck help <subcommand>` is rewritten to `<subcommand> -h` for the 15 subcommands from `spawn` to `helpers` (not `hook` or `statusline`). Any other word prints the top-level usage.

## Behaviour

- Every subcommand except `chat` first calls `useConsole()`, which on Windows attaches the parent terminal (no-op on Unix).
- A subcommand error prints `flockdeck <name>: <error>` to stderr and exits 1, unless the error is `errReported` (the flag set already printed it). `chat` and `keys` always print the error.
- A leftover argument after the flags prints `unrecognised argument "<arg>"` and the usage, and exits 2. It adds a hint when the argument is an existing directory (`flockdeck -C <dir>`) or a flag name.
- An unknown `-agent` id exits 2. An agent that is not installed prints a warning and continues.
- `-quit` with no instance running prints "nothing is running, so there is nothing to stop" and exits 0. Otherwise it reads the instance record in the state directory, checks the instance with `server.Identify`, and asks it to quit.
- A startup failure calls `fail`: it prints to stderr, appends to `error.log` in the state directory, shows an error box where the platform has one, and exits 1.
- Without `-solo`, a running instance is joined: the directory is opened in it, and `-new`, `-shell` and `-agent` are reported as ignored.
- Ctrl+C or SIGTERM starts a shutdown. A second signal, or a shutdown past the 10 second grace, forces exit 1.
- The usage text also lists `FLOCKDECK_UPDATE`, `FLOCKDECK_ALLOWED_HOSTS`, `FLOCKDECK_RELAY` and `FLOCKDECK_API_KEY`. Of these the root package reads only [FLOCKDECK_UPDATE](/config/flockdeck-update.md) and [FLOCKDECK_RELAY](/config/flockdeck-relay.md); the other two are read in internal packages.
