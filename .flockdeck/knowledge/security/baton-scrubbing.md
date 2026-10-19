---
type: Security Boundary
title: Baton scrubbing and prompt fencing
description: The layers that remove secrets from a baton and keep its text from being read as instructions or markup.
resource: internal/baton/scrub.go
tags: [security, secrets, baton, prompt-injection]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/scrub.go, internal/baton/shapes.go, internal/baton/shapes2.go, internal/baton/shapes3.go, internal/baton/shapes4.go, internal/baton/entropy.go, internal/baton/wrap.go, internal/baton/wraprun.go, internal/baton/clean.go, internal/baton/baton.go]
---
# Baton scrubbing

The scrubber is a filter, not a guarantee: a secret it was not told, with no known shape and not random-looking, gets through (code comment).

## Layers (`Scrubber`)

1. **Told values.** `NewScrubber(exact...)` takes values at least 8 characters long (`minExact`); shorter ones are ignored as words or ports. Sources: `EnvValues` (variables whose name `record.SecretName` calls secret) and `EnvFileValues` (`.env*` files in the directory and the repository root, regular files up to 1 MiB, comments and plain words or digit-only values left out). Values are held in memory for one run and never stored.
2. **Shapes.** Provider key forms, Authorization headers (the scheme stays, the credential goes), command-line and config forms, and tokens split across lines by terminal wrapping (`wrap.go`, `wraprun.go`).
3. **Entropy.** Long random-looking runs, decided a piece at a time so a URL loses only the key-like part. Paths, URLs, import paths, branch names and commit ids are protected from false positives.

Matching works on a normalised copy: invisible characters removed, full-width ASCII folded, odd spaces read both ways; spans map back to the original. A removed value is replaced by `[REDACTED: <kind>]` and counted in the baton's `redacted:` front matter. Forged marks are neutralised so repeating a scrub does not grow text. A scrub can have a deadline; text not reached is replaced by a mark.

## Fencing

- `CleanText` strips terminal escape sequences, control characters and zero-width characters, so `baton show` cannot move a cursor or hide text.
- `escapeTags` / `escapeBody` stop text from opening or closing the `<baton>` fence or a section heading, including look-alike brackets.
- `Frame` prepends a preamble saying the notes are claims to check and quoted tool output is data, not instruction.
- Batons are stored in a 0700 folder with 0600 files, never in a checkout.
- Sending a baton to an agent of another company needs the user's approval ([spawn](/api/hooks-spawn.md) `batonElsewhere`).
