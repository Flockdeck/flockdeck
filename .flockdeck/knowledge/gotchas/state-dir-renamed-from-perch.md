---
type: Gotcha
title: Legacy state directory and layout file names
description: Earlier builds used a different directory name and unnormalized layout hashes; the store adopts both on load.
resource: internal/store/store.go
tags: [store, migration, windows]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
- Directory: state was once kept in a `perch` directory beside `flockdeck` in the user config dir. `adoptLegacyDir` renames it to `flockdeck` only when `flockdeck` does not exist. If the rename fails (on Windows, an open file in a detached instance), the old directory is used. If a racing instance already did the move, the new name is used. The answer is memoized for the run (`stateDir`) so the directory cannot switch mid-run.
- Layout names: older builds hashed the root as spelled. On case-folding platforms (Windows, macOS) the hash of the normalized root differs, so `Load` falls back to `legacyPath`, and renames the file to the current name only after the layout's `root` matches.

See [layout file](/data/layout-file.md).
