---
type: Gotcha
title: Conflict radar throttles and fail-safes
description: When the workspace runs the conflict radar, and how it backs off on slow or vanished checkouts.
resource: internal/workspace/radar.go
tags: [radar, git, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/radar.go]
---
- Runs only when the `conflictRadar` preference is on ([prefs](/data/prefs-file.md)) and the installed git supports in-memory `merge-tree` (probed in the background; an unknown version does not count). Default is off.
- Pane chips (`Pane.Conflicts`) appear only after a pair has been seen to conflict twice. Refreshes are 15 s apart; a refresh that finds the previous one still running in a repository does nothing.
- One repository's snapshots and merges get 30 s per refresh (`radarDeadline`); a snapshot that times out is "unknown", and the radar keeps what it showed.
- Candidate base branches are cached up to 10 minutes (`radarBasesMaxAge`) because the freshness check does not notice a newly created `main`; "no base found" is believed for 5 s.
- A checkout whose git commands time out 3 times in a row is left alone for 10 minutes, doubling up to 2 hours; strikes are forgotten after 24 hours without a timeout, and only count if the checkout's own snapshot ran at least 15 s.
- A checkout's top folder must be found missing at 3 refreshes at least 5 s apart before the radar forgets its pairs and sightings (a drive reconnecting is not a deletion).
- Scratch directories left by a killed run are swept once per process (`gitx.SweepScratch`); `Close` waits up to 10 s for refreshes to remove theirs.
- Snapshot refusals (too many files, mid-merge, shallow clone) are listed in [radar snapshot](/data/radar-snapshot.md).

See [workspace](/packages/workspace.md).
