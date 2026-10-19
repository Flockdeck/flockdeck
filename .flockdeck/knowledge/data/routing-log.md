---
type: File Format
title: routing.jsonl
description: Legacy log of model-routing decisions, read back for rule statistics and clearable from Settings.
resource: internal/route/log.go
tags: [routing, log, jsonl]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/route/log.go]
---
# routing.jsonl

In the state (config) directory. No current code writes it: the code that did (Fan out) is gone, and the file is kept readable so Settings can show per-rule statistics (`route.Stats`) and clear it (`route.ClearLog`). Lines that do not parse are skipped.

| JSON | Meaning |
|---|---|
| `at`, `kind`, `project`, `pane` | When, `spawn` or `turn`, project, pane |
| `agent`, `baselineAgent` | Agent the work ran on; agent it would have run on, only set when a rule moved it to another agent |
| `source`, `rule` | `rule` or `fallback`; the rule name |
| `baseline`, `routed` | Model without routing, model chosen |
| `outcome` | `kept` or `overridden:<model>` |

It holds rule names and model ids, never the task or prompt. Stats count `kept` and `overridden:` per rule, skip rows with no rule name, and sort worst override rate first.
