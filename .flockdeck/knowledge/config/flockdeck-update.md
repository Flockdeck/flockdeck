---
type: Environment Variable
title: FLOCKDECK_UPDATE
description: Set to "off" to stop background checks for new releases.
resource: cli_update.go
tags: [config, update]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_update.go, internal/store/prefs.go]
---
Compared case-insensitively with "off". When it is, the running instance never checks for or stages updates, and the "check for updates" action in the window answers that the variable is set. The "updates off" preference has the same effect for the background check, but can be turned back on in the window; the variable cannot.

[update](/cli/update.md) still works when it is set.
