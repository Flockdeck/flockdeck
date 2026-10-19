---
type: Developer Tool
title: hooktest
description: Starts one Claude pane and prints every status transition reported back through the hook server.
resource: cmd/hooktest
tags: [dev-tool, hooks]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/hooktest/main.go]
---
Development aid, not shipped. Verifies end to end that a Claude pane reports its lifecycle to Flockdeck's hook server.

| Flag | Default | Meaning |
|---|---|---|
| `-hookbin` | none, required | Path to a built `flockdeck` binary (must exist, not a directory). |
| `-prompt` | "Reply with exactly the word OK and nothing else." | Text typed into the pane. |
| `-watch` | 90s | How long to watch for transitions. |

Flow: builds a `workspace` rooted at the current directory with `HookBinary` set, exits if the `claude` CLI is missing, opens a Claude tab, prints and dumps the generated `<pane id>.settings.json` from the sessions dir, waits 6 s, types the prompt, then polls `Sess.Status()` and prints transitions. It ends when the pane exits or goes working then idle, and reports which of working, waiting and idle were seen.

It uses the real state directory through `store`, not an isolated one. No workflow or Makefile target calls it.
