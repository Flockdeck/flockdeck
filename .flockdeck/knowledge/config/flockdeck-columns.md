---
type: Environment Variable
title: FLOCKDECK_COLUMNS
description: Forces the line width of the chat client's output.
resource: internal/chat/render.go
tags: [chat, terminal]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/chat/render.go]
---
- **Default:** unset.
- **Read by:** the chat client's width lookup. Order: `FLOCKDECK_COLUMNS`, then the terminal's own size, then `COLUMNS`, then a built-in default. Only integers of 20 or more count.
- **Why this order:** the terminal is asked before `COLUMNS` because it follows a pane being resized, while a shell sets `COLUMNS` once. No code sets `FLOCKDECK_COLUMNS` for a pane.
- Related: `NO_COLOR` set to anything, or `TERM=dumb`, turns colour off; so does stdout not being a console.
