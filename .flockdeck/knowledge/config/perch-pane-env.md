---
type: Environment Variable
title: PERCH_ pane variables
description: Deprecated PERCH_ names for the FLOCKDECK_ variables a pane carries.
resource: internal/legacyenv/legacyenv.go
tags: [env, deprecated]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/legacyenv/legacyenv.go]
---
- **Pattern:** `PERCH_<name>` as the old spelling of `FLOCKDECK_<name>` (the program was renamed). The code looks up `TOKEN`, `AGENT`, `API` and `PANE`.
- **Precedence:** `FLOCKDECK_<name>` wins when non-empty; the old name is the fallback.
- **Warning:** commands a person runs (`GetWarn` with stderr) print one line per name per process: "PERCH_X is deprecated and will be removed after 17 April 2027; set FLOCKDECK_X instead". Hook and status-line commands use the silent `Get` to keep stderr clean.
- **Why kept:** panes started by an earlier build keep only the old names after the binary is upgraded.

See [legacyenv](/packages/legacyenv.md) and [state dir renamed from perch](/gotchas/state-dir-renamed-from-perch.md).
