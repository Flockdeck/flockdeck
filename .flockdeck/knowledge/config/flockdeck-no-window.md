---
type: Environment Variable
title: FLOCKDECK_NO_WINDOW
description: Environment form of the -no-window flag: serve without opening a window.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-no-window`. True only for "1" or "true" (case-insensitive). With it, the instance prints its URL and keeps serving; attaching to a running instance prints the URL instead of opening a window. A flag wins.
