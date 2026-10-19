---
type: Security Boundary
title: Baton entropy detector
description: The third scrubbing layer, which removes long random-looking runs while keeping paths, URLs, identifiers and hashes.
resource: internal/baton/entropy.go
tags: [security, secrets, baton, entropy]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/entropy.go, internal/baton/scrub.go]
---
# Baton entropy detector

Layer three of [baton scrubbing](/security/baton-scrubbing.md). `entropySpans` returns byte ranges that `Scrubber` marks as `high-entropy`. The comments state the trade: a false positive costs the person an edit, a false negative costs a secret, but a baton is full of paths, import paths, branch names and commit ids that must survive.

## Candidates

`wordRe` finds runs of 24 or more ASCII characters with no space, quote, bracket, comma, semicolon, backtick or bar. Any non-ASCII character ends a run. Each run is then judged a piece at a time:

- A URL (`scheme://`): the scheme and host are kept; the rest is split at `/ ? & = # :` and each part of 24+ bytes is tested.
- Otherwise the run is split at `=`; a part that is a path (drive letter, `%VAR%\`, UNC, or `pathLike`) goes to `pathSpans`; others are split at `:` and tested (`pieceSpans`), with dotted pieces split at dots.
- `pathSpans` splits at `/` and `\`. A part of 24+ bytes (`pathPartMin`) is taken whole if it is random, else split at `.`, `:` and `@` and the pieces tested.

## randomRun

A token under 24 bytes is never random. A hex string of 64 or more characters is (a SHA-256 or image digest). Hex of 40 (commit id) or 32 characters is kept in practice because it fails step 2 below when it has no upper case letter. Then:

1. `nameLike` or `identLike` tokens are kept: words in lower case, Capitalised, camelCase or UPPER with digits only at the end (`acronymWord` allows camelCase with acronyms: three or more words averaging over three letters), and qualified identifiers whose letter runs are name words with at most three digit runs of at most three digits.
2. The token must contain upper case, lower case and a digit.
3. Shannon entropy in bits per byte must be at least 4.0, or 4.2 for tokens of 32 or more bytes.

## Paths kept whole

`pathLike` is true for a known root (`/usr`, `/etc`, `/home`, `/var`, `/tmp`, `/Users`, `/Library`, `/Volumes`, and others), a `./`, `../`, `~/` prefix (or backslash forms), a leading host-like part among the first three (`github.com/...`), or when every part, or at least three fifths of parts with three or more parts, is a name or short hash/UUID. A token containing a slash that is `pathLike` is not random (`looksRandom`). A UUID or short hash inside a path stays.

Related shared helpers (`mixedClasses`, `nameLike`, `identLike`, `isHex`) are also used by [baton secret shapes](/security/baton-secret-shapes.md) to decide whether a quoted value looks chosen.
