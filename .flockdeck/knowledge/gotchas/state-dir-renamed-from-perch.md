---
type: Gotcha
title: Legacy state directory and layout file names
description: Earlier builds used a different directory name and unnormalized layout hashes; the store no longer reads either.
resource: internal/store/store.go
tags: [store, migration, windows]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/store/store.go]
---
- Directory: state was once kept in a `perch` directory beside `flockdeck` in the user config dir. Nothing reads or moves it now: `Dir` is always `<config dir>/flockdeck`, and a leftover `perch` directory is ignored where it stands (never deleted). A user upgrading from a build before the rename opens an empty workspace; their old state is still under `perch` and can be moved by hand.
- Layout names: older builds hashed the root as spelled, not normalized. Nothing falls back to that name now. On case-folding platforms (Windows, macOS) a layout never saved by a build since normalization sits unread beside the current name, and the project opens empty. The file is not touched.

See [layout file](/data/layout-file.md).
