---
type: CLI Command
title: flockdeck recordings
description: Lists recorded pane transcripts and exports an agent's stored conversation as one.
resource: cli_recordings.go
tags: [cli, recordings]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_recordings.go]
---
## Synopsis

- `flockdeck recordings [-dir] [-json]`
- `flockdeck recordings export [-o file] [-reveal] <pane-id | conversation-id>`

## List flags

| Flag | Default | Effect |
|---|---|---|
| `-dir` | false | Print only the folder recordings are kept in. |
| `-json` | false | One JSON object per line, one per recording. |

The default listing is newest first: start time, project, the first 8 characters of the conversation id, size (B, KiB or MiB) and file path. With none it prints "no recordings" and the folder. Positional arguments are an error ("recordings takes no arguments, only flags"). Exports are not listed.

## export

| Flag | Default | Effect |
|---|---|---|
| `-o <file>` | the project's exports folder under the recordings folder | Write here. The file must not exist and must not be inside the project or a git repository (`record.CheckExportPath`). |
| `-reveal` | false | Show the written file in the file manager. |

The id is a pane id from the saved layouts of any known project, or a Claude Code conversation id (a file name under its projects folder). Only Claude Code stores a conversation Flockdeck can read; for other agents nothing is written. Flags may follow the id.

Output: `exported N lines (P prompts, M messages, T tool calls) to <path>`, notes if an earlier export was replaced, the transcript hit its size cap (`record.MaxFileBytes`), or entries were skipped, and a reminder that secrets removal is best effort. If an earlier export holds events the new one lacks, nothing is written and the command exits 1 with an explanation. Files are written 0600 on Linux and macOS.

## Where it runs

Any shell. It reads the state directory directly and needs no running instance ([record package](/packages/record.md)).
