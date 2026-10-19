---
type: Security Boundary
title: Spawn limits, approved targets and inherited auto-review
description: Caps on agent-started helpers and checks that what a user approved is what runs.
resource: internal/workspace/spawn.go
tags: [security, spawn, workspace]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/workspace/spawn.go]
---
- Helper cap: `MaxHelpers` = 8 open panes below any one agent that started them with `flockdeck spawn`, counting grandchildren and exited helpers until closed. `HelperRoom` checks the parent and every ancestor (walk bounded to 16, using `Pane.Ancestors` so closing a middle pane does not reset the count). `Spawn` re-checks under the lock that adds the pane so two starts cannot take the last slot. A start from outside any pane has no limit.
- Approved target: a baton handoff carries an `ApprovedTarget` (agent, provider, source provider, whether approval was required, extra folders). `checkApproved` recomputes the provider in the real working folder and refuses if the agent or company changed since the user was asked, or if no approval was needed and the folder's settings now route to a different company.
- Baton text is resolved and scrubbed by the caller; a task string cannot carry unscrubbed text past the scrubber.
- Auto-review inheritance: a helper started from the window inherits its parent's `AutoReview`; a helper started by an agent (`SpawnedByAgent`) does not, and takes the installation default, so an unattended agent cannot hand its helpers self-approval. Auto-review only answers calls that would be prompted anyway and never denies.
- A role is recorded by id; its content is read from the catalog each time the pane is briefed.
- A locked pane (`Locked`) refuses close from the keyboard, header button, tab close, "close finished panes" and `flockdeck close`.

See [workspace](/packages/workspace.md), [provider detection](/security/provider-gateway-detection.md).
