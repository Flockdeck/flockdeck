---
type: Gotcha
title: Radar conflicts appear after two refreshes
description: The conflict radar waits for repeated sightings and rate limits merges, so a conflict is not shown the instant it exists.
resource: internal/radar/engine.go
tags: [radar, git, debounce]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/radar/engine.go]
---
- A pair must conflict in 2 consecutive counted refreshes (`confirmAfter`) before it is reported: an agent mid-edit can conflict for a few seconds, and a chip that comes and goes teaches people to ignore it.
- Refreshes within `MinGap` (default 5 s) of the last counted one do not count again, because refreshes are also requested out of turn by a commit or a pane opening.
- Merges are capped at 50 per refresh and the walk resumes from the first pair left behind, otherwise pairs at the end of a large set would never be reached. `Report.Behind` is logged at most once a minute.
- A snapshot that failed or timed out (`Unknown`) leaves its pairs as they were rather than clearing them.
- Results are cached by tree and head ids, which are content addressed, so a cached entry is never wrong; eviction is least recently used.
- Pairs with disjoint changed paths run no merge. A file moved out of a directory (`Dirs`) still meets another checkout's path under that directory.
