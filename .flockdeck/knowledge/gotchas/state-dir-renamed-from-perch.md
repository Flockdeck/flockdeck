---
type: Gotcha
title: Legacy state directory and layout file names
description: Earlier builds used a different directory name and unnormalized layout hashes; the store adopts both on load.
resource: internal/store/store.go
tags: [store, migration, windows]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
- Directory: state was once kept in a `perch` directory beside `flockdeck` in the user config dir. Nothing reads or moves it now: `Dir` is always `<config dir>/flockdeck`, and a leftover `perch` directory is ignored where it stands (never deleted). A user upgrading from a build before the rename opens an empty workspace; their old state is still under `perch` and can be moved by hand.
- Layout names: older builds hashed the root as spelled. On case-folding platforms (Windows, macOS) the hash of the normalized root differs, so `Load` falls back to `legacyPath`, and renames the file to the current name only after the layout's `root` matches.

See [layout file](/data/layout-file.md).
