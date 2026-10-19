---
type: Gotcha
title: A baton must fit a Windows command line
description: The baton prompt is a command-line argument, so its size is budgeted below the 32,767-character Windows cap.
resource: internal/baton/frame.go
tags: [baton, windows, limits]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/baton/frame.go]
---
- The framed baton is passed to the agent as an argument. Windows caps a whole command line at 32,767 characters, so `BudgetBaton` is 8 KiB and `BudgetTotal` (baton, framing and task) is 24 KiB, leaving room for the pane briefing and other arguments. A large task still leaves the baton at least 2 KiB (`minRoom`).
- Cutting goes in a fixed order and never inside a line: `Files touched`, `Commands and tests run` and `Not in your checkout` are shortened by whole lines (with a count of omitted lines), then `Where things stand`, `Decisions and why`, `Open questions` and `Constraints` are dropped whole in that order; if only `Goal` is left it is cut at a line.
- When anything is cut, the prompt says what is missing and where the rest is: `Framed.Truncated` tells the caller to write `Full` to an overflow file in the checkout, which the store tracks for later deletion ([baton store](/data/baton-store.md)).
