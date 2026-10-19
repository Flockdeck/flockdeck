---
type: CLI Command
title: flockdeck helpers
description: Installs, uninstalls, starts, stops and opens helper apps.
resource: cli_helpers.go
tags: [cli, helpers]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_helpers.go, main.go, helpers_run.go, internal/server/helpers.go, internal/helpers/install.go, internal/helpers/supervisor.go]
---
## Synopsis

`flockdeck helpers <command> ...`. The only helper in the catalogue is `lens`.

## Commands

| Command | Flags | Needs running instance | Effect |
|---|---|---|---|
| `list` | `-check` | no | Table of helper, installed version, status (not installed, stopped, running with process id) and notes. `-check` asks each source for a newer version. |
| `install <helper>` | `-version <v>` (a leading "v" is stripped), `-yes` | no | Looks up the release, shows source URL, signature note, SHA-256 and what the helper may do, and asks "[y/N]". Downloads and installs. May repair a copy that no longer matches. |
| `uninstall <helper>` | `-purge-data`, `-yes` | no | Removes the helper, keeping its data folder unless `-purge-data`. Refuses while it runs. |
| `start <helper>` | none | yes | Starts it and prints the URL, or the state, error and log lines if it did not reach running. |
| `stop <helper>` | none | yes | Stops it. |
| `open <helper>` | none | yes | Opens its page in the browser. |

`-h`, `--help` and `help` print the usage to stdout and exit 0. Flag-level `-h` also exits 0.

## Behaviour

- Install and uninstall prompt only on a terminal; otherwise they fail telling you to pass `-yes`.
- A bad or missing signature, a checksum mismatch or an unsigned release where signing is required stops the install, and nothing overrides it. A release with no build for this platform, or a helper that is running, gets its own message.
- `start`, `stop` and `open` find the instance through `runningInstance` and send `server.HelperRequest`; with none running they fail saying to open Flockdeck first.
- A hidden `ctrl-break <pid>` form exists for the Windows supervisor.
- Unknown helper ids fail listing the known ones. An unknown command prints the usage and exits 1.

## Where it runs

Any shell. State lives in the helpers store under the state directory ([helpers package](/packages/helpers.md)).
