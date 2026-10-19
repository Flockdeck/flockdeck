---
type: File Format
title: Helper apps directory layout
description: On-disk layout of installed helpers under the config dir's apps folder.
resource: internal/helpers/layout.go
tags: [helpers, store]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/helpers/layout.go]
---
Under `<config dir>/apps/<id>/`:

| Path | Content |
|---|---|
| `versions/<v>/` | Unpacked release: the binary, `install.json` and whatever else the archive holds |
| `current.json` | `{"version":"x.y.z"}`, written last by an install |
| `data/` | The helper's `DATA_DIR`; an uninstall leaves it alone |
| `logs/helper.log`, `.1` to `.3` | Rotating log |
| `run.json` | Only while running: `pid`, `started`, `port`, `startedAt`, `ownerPid`, `ownerStarted`, `starting` |
| `staging.new-*/` | Install in progress, swept when old |
| `trust.json` | Per-helper trust record with the high-water version |

`install.json` (`InstallInfo`) records `version`, `source`, `sha256` (archive), `binarySha256` (checked before every start) and `installedAt`. Paths are built only from validated ids and versions, never from archive or helper content.

A startup reaper stops helpers recorded in `run.json` by an earlier Flockdeck, unless another instance is running.

See [helpers](/packages/helpers.md), [verification](/security/helper-install-verification.md).
