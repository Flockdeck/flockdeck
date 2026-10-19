---
type: Environment Variable
title: FLOCKDECK_SOLO
description: Environment form of the -solo flag: always start a new instance.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-solo`. True only for "1" or "true" (case-insensitive). A flag wins. A solo instance beside a running one is not the one `flockdeck` and `flockdeck -quit` reach.
