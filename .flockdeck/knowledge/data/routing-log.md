---
type: File Format
title: routing.jsonl
description: Legacy log of model-routing decisions from an older build. Nothing reads, writes or deletes it.
resource: internal/server/routing.go
tags: [routing, log, jsonl]
timestamp: 2026-11-16T00:00:00Z
code_paths: []
---
# routing.jsonl

In the state (config) directory. Fan out wrote it; that code is gone. Flockdeck no longer reads it either (Settings used to show per-rule override rates from it and had a button to clear it), and it never deletes it: the file is the user's. Settings > Data in the help says so.

Each line was one decision: `at`, `kind`, `project`, `pane`, `agent`, `baselineAgent`, `source`, `rule`, `baseline`, `routed` and `outcome` (`kept` or `overridden:<model>`). It held rule names and model ids, never the task or prompt.

The `clearRoutingLog` command is still accepted so a client built against API 1.x gets an answer; it only sends a notice. It can be removed with the next major API version.
