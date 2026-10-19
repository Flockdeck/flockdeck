---
type: File Format
title: Baton document
description: Markdown handoff with front matter and eight fixed sections, written by Render and read by Parse.
resource: internal/baton/baton.go
tags: [baton, handoff, markdown]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/baton.go]
---
# Baton document

## Id

`YYYYMMDD-HHMMSS-<6 hex>` (UTC time, then a random 3-byte suffix). `ValidID` checks the shape with a regexp; anything else is treated as a pane id or a path, never joined to the baton folder.

## Layout

```
---
id: ...
created: <RFC 3339 UTC>
pane: / agent: / model: / cwd: / branch: / base: / derived: a, b / redacted: kind=n, ...
---

# Baton: <title or "Untitled">

## Goal
...
```

Front matter is one `key: value` per line, values forced to one line. Empty keys are omitted. `derived` lists parent baton ids, oldest first; `redacted` counts what the scrubber removed by kind (recomputed by scrub, not trusted from disk).

## Sections (in order)

`Goal`, `Where things stand`, `Decisions and why`, `Files touched`, `Commands and tests run`, `Open questions`, `Constraints`, `Not in your checkout`. An empty section is not written. `_Not filled in._` is the placeholder; `Frame` leaves a section that still holds it out of the prompt.

## Escaping

On render, any line in a body that equals a known `## Section` heading gets a backslash prefix, and text that looks like the `<baton>` fence (including full-width or look-alike brackets and invisible characters) is turned into `&lt;...`. `Parse` reverses both, removes escape sequences and control characters, ignores text before the first known heading, and keeps an unknown heading as text of the section above.

## Fork

A baton is not changed once saved or started from. `Fork` makes a copy with a new id and appends the old id to `derived`. See [baton store](/data/baton-store.md) and [baton](/packages/baton.md).
