---
type: Gotcha
title: Remote windows neither keep the app alive nor end it
description: Only windows on this machine count towards the last window going; relay windows are excluded.
resource: internal/server/remote.go
tags: [remote, lifecycle]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/remote.go]
---
`LocalClientCount` counts control clients whose `remote` flag is false. When the count reaches zero, `OnLastClientGone` runs and an instance that is not detached quits. A relay window closing never triggers that, and an open one does not prevent it.

To leave agents running for a phone to find, the desk detaches (`detach` command). From a relay window `detach` only returns a notice, because marking the instance detached would stop the desk window's close from quitting, and a page not opened by script cannot close itself.

`/health` reports `windows` as `LocalClientCount()` for the same reason: a second launch finds a local window to bring back rather than opening another. `viewers` in the snapshot's `remote` object uses `remoteClientCount()`.
