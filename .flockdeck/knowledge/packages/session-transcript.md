---
type: Package
title: session transcript readers
description: The transcript subpackage that finds, lists, tails and exports stored agent conversations, with a Claude Code reader.
resource: internal/session/transcript/transcript.go
tags: [transcript, history, resume, claude]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/session/transcript/transcript.go, internal/session/transcript/claude.go, internal/session/transcript/claude_replies.go, internal/session/transcript/claude_export.go, internal/session/transcript/claude_stream.go, internal/session/transcript/export.go, internal/session/transcript/stream.go, internal/session/transcript/diff.go, internal/session/transcript/image.go]
---
# internal/session/transcript

Three callers read stored conversations: layout restore (resume only if something exists), the history overlay, and fan-out (takes a plan from an agent's last replies). All go through a `Reader` with `Path`, `Replies` and `Conversations`.

## Choosing a reader

`For(spec)` returns `Null` unless `spec.Caps.Transcript` is set. Agents are looked up by program name (`program(spec)`: lower-cased, folder and `.exe/.cmd/.bat/.com/.ps1` removed), so a second Claude account running `claude` with its own config dir still gets `Claude`. Only `claude` is registered. `Null` answers "none" to everything.

`Exists(spec, id)` needs `Caps.Resume`, a path, and a file larger than zero bytes; an empty file means the session never recorded anything and resuming it would make the agent exit.

`All(specs, cwd)` merges conversations across agents, newest first (id breaks ties). Two Claude entries sharing one home folder are listed once. The first error is returned beside whatever was found.

## Claude Code files

- Home is `CLAUDE_CONFIG_DIR` or `.claude` in the user's home. An agent entry's own env wins over Flockdeck's ([CLAUDE_CONFIG_DIR](/config/claude-config-dir.md)).
- `claudePath(home, id)` searches every folder under `projects/` for `<id>.jsonl`, preferring non-empty over empty, then the newest. It lists folders instead of globbing, so brackets in a path do not break it. Ids containing `/` or `\` are refused.
- `projectSlug` reproduces Claude's folder naming: every UTF-16 unit that is not an ASCII letter or digit becomes `-`; past 200 characters the name is cut and a base-36 hash of the path appended.
- Listing reads the opening entries for a prompt (`summaryScanLimit` 200 entries), a title and the cwd, and caches per folder (`cachedFolderLimit` 64 folders), keyed on size and modification time.
- `claudeReplies` reads at most the last 4 MiB (`replyTailBytes`), ignoring sidechain and meta entries.
- `Follower` (`claude_export.go`) polls a file in 256 KiB chunks and yields agent-neutral `ExportEvent` values (prompt, message, tool call, tool result, title, compact) with model, token usage once per reply and stop reason. `ErrReplaced` means the file shrank; `ErrRead` is retryable; `ErrNoTranscript` means nothing stored. It feeds [record](/packages/record.md) exports.

## Live streams and diffs

`StreamFor(spec, id)` gives a `Stream` (Claude implements it) that tails a file into [transcript entries](/data/transcript-entry.md). `SetLight(true)` keeps nothing in memory for panes nobody views. `EditDiff`, `MultiEditDiff` and `WriteDiff` build unified diffs; above 1500 lines on either side the diff is coarse (every line changed) instead of an LCS comparison. Images are allowed as png, jpeg, gif or webp only, refused above 20 MiB input or 24 million pixels, downscaled to 2000 px, and sent under 700 KiB.

Related: [session](/packages/session.md), [workspace](/packages/workspace.md).
