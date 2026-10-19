---
type: Environment Variable
title: FLOCKDECK_FRESH
description: Environment form of the -new flag: start with a single pane instead of the saved layout.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-new`. True only for "1" or "true" (case-insensitive, whitespace trimmed); anything else, including "0" and "false", is false. A flag on the command line wins.
