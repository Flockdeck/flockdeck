---
type: CLI Command
title: flockdeck update
description: Fetches the latest release, or a named one, verifies it and puts it in place.
resource: cli_update.go
tags: [cli, update]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_update.go]
---
## Synopsis

`flockdeck update [-check] [-version=<release> [-yes]]`

## Flags

| Flag | Default | Effect |
|---|---|---|
| `-check` | false | Report whether a newer release exists and stop. With `-version`, report what installing it would do. |
| `-version <release>` | none | Install this release instead of the latest, forward or back (for example v1.4.0). Asks first. |
| `-yes` | false | With `-version`, skip the confirmation. |

Positional arguments are an error, with a hint if one names a flag.

## Behaviour

- A build whose version does not parse as a release (a source build) prints that it has nothing to compare with and exits 0.
- Warns first if the running version has been recalled, and names a fixed version if the recall gives one.
- Without `-version`: finds the latest release; if it is not newer, says so. Otherwise downloads it (reusing an already staged copy of that version), verifies it, and applies it over the running program with `selfupdate.Apply`. A running instance keeps going; the new version is used from the next start.
- With `-version`: refuses a draft release, labels the action Installing, Rolling back to or Reinstalling, and asks "[y/N]". Without a terminal and without `-yes` it fails.
- If the program file cannot be written, the error says the download is staged and to retry with sudo (administrator shell on Windows).
- Network failures are reported as being unable to reach the download host or GitHub. The overall timeout is 10 minutes.

## Background checking

A running instance also checks every 6 hours and stages newer releases. [FLOCKDECK_UPDATE](/config/flockdeck-update.md)`=off`, or the "updates off" preference, stops that. It does not stop this command.
