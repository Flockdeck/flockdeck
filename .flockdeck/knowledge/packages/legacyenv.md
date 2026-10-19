---
type: Package
title: legacyenv
description: Reads pane environment variables under the FLOCKDECK_ name or the old PERCH_ name.
resource: internal/legacyenv
tags: [env, compatibility]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/legacyenv]
---
`Get(name)` returns `FLOCKDECK_<name>`, else `PERCH_<name>`, silently. `GetWarn(w, name)` does the same and, when the old name is the one used and `w` is not nil, writes one deprecation line per name per process. `RemovalDate` is "17 April 2027".

Used by the root program (`paneEnv` warns on stderr; `quietPaneEnv` is for hook and status-line commands that run on every event) and `internal/chat`. Names looked up include `TOKEN`, `AGENT`, `API`, `PANE`.

Reason: a pane started by an earlier build still has only the old names in its environment after the binary on PATH is upgraded. See [PERCH_ pane variables](/config/perch-pane-env.md).
