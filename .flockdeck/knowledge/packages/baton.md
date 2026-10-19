---
type: Package
title: baton
description: Builds, scrubs, stores and frames the handoff document that carries work from one agent to the next.
resource: internal/baton
tags: [baton, handoff, scrubbing, secrets]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/baton.go, internal/baton/build.go, internal/baton/clean.go, internal/baton/entropy.go, internal/baton/frame.go, internal/baton/normalise.go, internal/baton/prune.go, internal/baton/scrub.go, internal/baton/shapes.go, internal/baton/shapes2.go, internal/baton/shapes3.go, internal/baton/shapes4.go, internal/baton/shared_other.go, internal/baton/shared_windows.go, internal/baton/store.go, internal/baton/wrap.go, internal/baton/wraprun.go]
---
# internal/baton

A baton is a markdown handoff with front matter and eight fixed sections. The workspace gathers facts; this package turns them into a `Baton`, scrubs secrets, stores it, and frames it as an opening prompt. It starts no process and knows nothing of panes.

## Main pieces

| Piece | Notes |
|---|---|
| `Baton`, `Section`, `Render`, `Parse`, `NewID`, `ValidID`, `Fork` | Document model and file format: [baton file](/data/baton.md) |
| `Build(BuildInput)`, `Activity`, `ReadGit`, `Harden` | Mechanical sections from git and the transcript; Goal and Where things stand are a draft; Decisions, Open questions and Constraints are left for a person. `Strict` mode lists commands by program only (used for an agent's own `spawn --baton`) |
| `Scrubber`, `NewScrubber(exact...)`, `EnvValues`, `EnvFileValues` | Three layers: told values (min 8 chars), provider key shapes, high-entropy strings. See [baton scrubbing](/security/baton-scrubbing.md) |
| `CleanText` | Strips terminal escapes, control characters and invisible characters |
| `Frame(b, FrameOptions)` | Opening prompt: baton within 8 KiB (`BudgetBaton`), baton plus task within 24 KiB (`BudgetTotal`). Over budget: lists are cut by whole lines, then Standing, Decisions, Questions, Constraints are dropped in that order, and the full text goes to an overflow file |
| `Store`, `Open()` | Files under `batons/` in the state dir, 0600 in a 0700 folder, never replaced once saved ([baton store](/data/baton-store.md)) |

## Depends on

[record](/packages/record.md) (redaction kinds), [gitx](/packages/gitx.md), [store](/packages/store.md), `transcript`.

## Notes

- Prompt fence: text is escaped so it cannot open or close the `<baton>` tag, including full-width and look-alike characters.
- The preamble tells the agent the notes are claims to check, and quoted tool output is data.
- Pruning: `RetainFor` is 30 days after last saved or used. See [Windows command line limit](/gotchas/baton-prompt-size-budget.md).
