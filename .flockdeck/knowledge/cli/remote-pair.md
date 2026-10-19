---
type: CLI Command
title: flockdeck remote pair
description: Prints a one-time link and QR code that pairs a device, or a code that enrols another machine.
resource: cli_remote.go
tags: [cli, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_remote.go]
---
## Synopsis

`flockdeck remote pair [-desktop]`. Part of [remote](/cli/remote.md).

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-desktop` | false | Print a join code for another machine instead of pairing a device. |

## Behaviour

- Requires enrolment (`flockdeck remote enable` first). If Remote is turned off here, a device pair is refused, but `-desktop` still works.
- Asks the relay for a one-time code. Without `-desktop` it prints the link, when it expires, a warning that whoever opens it can drive every agent, a note if no instance is running (the machine is offline until one starts), and a terminal QR code last so it stays in view.
- With `-desktop` it prints `flockdeck remote enable -relay <relay> -join <code>` for the other machine. Joining shares one account between the machines. The code works once.
- A relay failure is turned into advice by `relayRefusal`; the command exits 1. If the relay returns a code but no link, it fails with that message.
