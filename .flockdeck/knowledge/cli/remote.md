---
type: CLI Command
title: flockdeck remote
description: Enrols this machine with a relay so other devices can reach its agents, and manages pairing, naming and removal.
resource: cli_remote.go
tags: [cli, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_remote.go]
---
## Synopsis

`flockdeck remote <command> [flags]`. With no command it prints the usage and exits 0. `remote help <command>` prints that command's help on stdout. The relay service itself is a separate private component.

## Commands

| Command | Flags | Effect |
|---|---|---|
| `enable` | `-relay <URL>` (default [FLOCKDECK_RELAY](/config/flockdeck-relay.md), else the built-in default relay); `-name` ([FLOCKDECK_REMOTE_NAME](/config/flockdeck-remote-name.md), else host name); `-join <code>` ([FLOCKDECK_REMOTE_JOIN](/config/flockdeck-remote-join.md)); `-invite <code>` ([FLOCKDECK_REMOTE_INVITE](/config/flockdeck-remote-invite.md)) | Enrols the machine. A relay that checks email prints a link and waits for verification; Ctrl+C cancels. Run on a machine that was disabled, it turns Remote back on for the same account. |
| `pair` | `-desktop` | See [remote pair](/cli/remote-pair.md). |
| `status` | none | Relay, machine name and id, state (connected, not connected, turned off, revoked), the address a device opens, and a summary of paired devices. Without enrolment it says so and which relay `enable` would use. |
| `devices` (aliases `list`, `ls`) | none | Lists paired devices with ids and names, and the account's machines. |
| `revoke <id or name>` | none | Unpairs a device and closes its open window. A multi-word name may be typed without quotes. |
| `rename <name>` | `-device <id or name>` | Renames this machine, or with `-device` a paired device. |
| `disable` | none | Turns Remote off here and stays enrolled; `enable` later reconnects to the same account. |
| `remove` | `-force`, `-yes` | Takes this machine off its account. The account and its other machines, devices and plan stay. Asks first unless `-yes`; with no terminal and no `-yes` it fails with "nothing has changed". `-force` forgets the enrolment even if the relay cannot be told. |
| `move <relay>` | `-invite`, `-join`, `-name`, `-yes` | Enrols with the new relay first, then leaves the old one once the new one answers. Devices must pair again. Probes the new relay before changing anything. |
| `delete-account` | `-yes -confirm <text>` | Erases the account on the relay. Previews what goes, then asks for the phrase "delete my account". A script needs both `-yes` and exactly `-confirm "delete my account"`. |
| `artifacts off` | none | Switches off every kind of artifact paired devices may view, in the preferences file, and tells a running instance to close its artifact sockets. There is no `on`. |

## Where it runs

Any shell. Enrolment is stored in a local config (`remote.Load`). Commands that change it also ask a running instance to reload (`server.RequestRemoteReload`); if none is running they say it will connect at next start. `status` and `devices` call the relay. Failures print `flockdeck remote: <error>`; an unknown command prints the usage and a spelling suggestion, exit 1.

## Disagreement with the root usage

The top-level usage omits `remote devices` (and its aliases) and `remote artifacts off`; `flockdeck remote` lists them.
