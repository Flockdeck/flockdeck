# Parity of the new interface with the old

This file is written by `go run ./cmd/parity -w`. Do not edit it: change `cmd/parity/classify.go` or the interface and write it again. A test in `cmd/parity` fails when this file is out of date, and when the old interface does something that is neither done, replaced, cut nor owned by a task.

It compares two lists the sources hold: the actions of the key table (`internal/help/keys.go`) against what the new shell runs, and the control-socket commands the old interface sends (`internal/webui/assets/app.js`) against the ones the new interface sends. It does not look at the pane sockets or the pages the old interface fetches, and it cannot see an old feature that has no action or command of its own. See [what this does not cover](#what-this-does-not-cover).

## Summary

| | Actions | Commands |
| --- | --- | --- |
| done | 38 | 96 |
| replaced | 0 | 3 |
| cut | 14 | 12 |
| planned | 6 | 45 |
| unclassified | 0 | 0 |
| total | 58 | 156 |

Done means the new interface runs the action or sends the command under that name. Replaced means it sends another command for it. Cut is on purpose. Planned names the task on the board that owns it.

## Cut on purpose

The old interface let a person tile, split, move and broadcast to panes. The new one shows a team as a graph and does not do these. Nothing else is cut.

| Kind | Name | Why |
| --- | --- | --- |
| action | `splitRight` | Tiling and splitting panes is cut; the team is a graph. |
| action | `splitDown` | Tiling and splitting panes is cut; the team is a graph. |
| action | `splitRightChoose` | Tiling and splitting panes is cut; the team is a graph. Starting an agent with a chosen agent is the start flow. |
| action | `splitRightShell` | Tiling and splitting panes is cut; the team is a graph. Starting a shell is the start flow. |
| action | `movePaneLeft` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `movePaneRight` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `movePaneUp` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `movePaneDown` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `movePaneToNewTab` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `tilePanes` | Tiling and splitting panes is cut; the team is a graph. |
| action | `mergeAllTabs` | Moving and merging panes and tabs is cut; the team is a graph. |
| action | `toggleBroadcast` | Broadcasting to panes is cut; a manager sends to its team. |
| action | `toggleBroadcastMember` | Broadcasting to panes is cut; a manager sends to its team. |
| action | `promptAll` | Broadcasting to panes is cut; a manager sends to its team. It opens the prompt bar aimed at the focused pane, which the new prompt (paneSend) covers; with broadcast on it reached every member. |
| command | `mergeAllTabs` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `mergeTab` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `movePane` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `movePaneDir` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `movePaneToNewTab` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `movePaneToTab` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `setWeights` | Tiling and splitting panes is cut; the team is a graph. |
| command | `splitPane` | Tiling and splitting panes is cut; the team is a graph. |
| command | `swapPanes` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `tilePanes` | Tiling and splitting panes is cut; the team is a graph. |
| command | `toggleBroadcast` | Broadcasting to panes is cut; a manager sends to its team. |
| command | `toggleBroadcastMember` | Broadcasting to panes is cut; a manager sends to its team. |

## Planned, by task

| Task | Actions | Commands |
| --- | --- | --- |
| T131 (C5, task detail) |  | `taskList` |
| T132 (C6, Changes) | `changes` |  |
| T133 (C7, worktrees) | `worktrees` | `conflictRadar`, `worktreeAdd`, `worktreePrune`, `worktreeRemove`, `worktrees` |
| T134 (C8, GitHub) |  | `ghChecks`, `ghInstall`, `ghIssue`, `ghIssueComment`, `ghIssueCreate`, `ghIssues`, `ghLogin`, `ghLoginCancel`, `ghLogout`, `ghPR`, `ghPRComment`, `ghPRCreate`, `ghPRs`, `ghStatus` |
| T136 (L1, start flows and pane lifecycle) | `selectTab`, `closeFinishedPanes` | `agents`, `closeFinishedPanes`, `focusPane`, `refreshAgents`, `teamClose`, `toggleZoom` |
| T137 (L2, settings) |  | `dismissTip`, `railExpanded`, `railWidth`, `resetKeybinding`, `resetKeybindings`, `setAgentAddress`, `setAgentDefault`, `setKeybinding`, `statusLine` |
| T138 (L3, help) |  | `helpSeen` |
| T143 (L8, remaining actions) | `agents`, `team` | `msgList`, `msgSeen`, `msgSend`, `taskAdd`, `taskAssign`, `taskBranch`, `taskNote`, `taskPrune`, `taskStatus` |

## Must exist before the old interface is retired

These are safety and security controls. The new interface has no way to do them yet, and T162 (the flip) must not remove the old interface until it does.

| Command | Owner | Gate | What it is |
| --- | --- | --- | --- |

## Actions of the key table

| Action | What it is | Status | |
| --- | --- | --- | --- |
| `splitRight` | Split right (agent) (Ctrl+Shift+D) | cut | Tiling and splitting panes is cut; the team is a graph. |
| `splitDown` | Split down (agent) (Ctrl+Shift+E) | cut | Tiling and splitting panes is cut; the team is a graph. |
| `splitRightChoose` | Split right (choose agent)… | cut | Tiling and splitting panes is cut; the team is a graph. Starting an agent with a chosen agent is the start flow. |
| `splitRightShell` | Split right (shell) | cut | Tiling and splitting panes is cut; the team is a graph. Starting a shell is the start flow. |
| `movePaneLeft` | Move pane left (Ctrl+Shift+←) | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneRight` | Move pane right (Ctrl+Shift+→) | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneUp` | Move pane up (Ctrl+Shift+↑) | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneDown` | Move pane down (Ctrl+Shift+↓) | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneToNewTab` | Move pane to a tab of its own | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `tilePanes` | Tile these panes evenly | cut | Tiling and splitting panes is cut; the team is a graph. |
| `zoomPane` | Zoom pane (Ctrl+Shift+Z) | done |  |
| `restartPane` | Restart pane | done |  |
| `lockPane` | Lock pane | done |  |
| `recordPane` | Start recording | done |  |
| `exportTranscript` | Export transcript | done |  |
| `revealTranscript` | Reveal transcript | done |  |
| `paneInfo` | Pane info (Ctrl+Shift+Y) | done |  |
| `closePane` | Close pane (Ctrl+Shift+W) | done |  |
| `newAgentTab` | New agent tab (Ctrl+Shift+T) | done |  |
| `newAgentTabChoose` | New agent tab (choose agent)… | done |  |
| `newShellTab` | New shell tab (Ctrl+Shift+N) | done |  |
| `nextTab` | Next tab (Ctrl+Tab) | done |  |
| `prevTab` | Previous tab (Ctrl+Shift+Tab) | done |  |
| `selectTab` | Select tab by number (Alt+1 … Alt+9) | planned | T136 (L1, start flows and pane lifecycle). The tab strip sends selectTab when a tab is pressed; the Alt+number key is not run |
| `mergeAllTabs` | Merge every tab into this one | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `moveTabLeft` | Move tab left | done |  |
| `moveTabRight` | Move tab right | done |  |
| `toggleBroadcast` | Toggle broadcast (Ctrl+Shift+B) | cut | Broadcasting to panes is cut; a manager sends to its team. |
| `toggleBroadcastMember` | Add this pane to broadcast, or take it out | cut | Broadcasting to panes is cut; a manager sends to its team. |
| `promptAll` | Prompt all panes (Ctrl+Shift+P) | cut | Broadcasting to panes is cut; a manager sends to its team. It opens the prompt bar aimed at the focused pane, which the new prompt (paneSend) covers; with broadcast on it reached every member. |
| `makeBaton` | Make baton: hand this pane's work to another agent | done |  |
| `agents` | All agents across projects (Ctrl+Shift+A) | planned | T143 (L8, remaining actions). The list of agents across projects has no task; decide whether the graph's project switch is enough |
| `closeFinishedPanes` | Close finished panes — idle agents and cleanly exited panes, in every open project | planned | T136 (L1, start flows and pane lifecycle) |
| `startTeam` | Start a team — a manager agent that starts the rest | done |  |
| `team` | Team — this project's roster, task board and messages (Ctrl+Shift+M) | planned | T143 (L8, remaining actions). The graph is the roster; the task board and messages dialog has no task |
| `apiKeys` | API keys… | done |  |
| `worktrees` | Worktrees (Ctrl+Shift+G) | planned | T133 (C7, worktrees) |
| `changes` | Review changes, commit and push (Ctrl+Shift+S) | planned | T132 (C6, Changes) |
| `palette` | Command palette (Ctrl+Shift+K) | done |  |
| `nextRegion` | Move to the next part of the window (F6) | done |  |
| `prevRegion` | Move to the previous part of the window (Shift+F6) | done |  |
| `findInTerminal` | Find in terminal (Ctrl+Shift+F) | done |  |
| `findPane` | Find pane (Ctrl+Shift+L) | done |  |
| `history` | Resume — reopen a past conversation (Ctrl+Shift+R) | done |  |
| `projects` | Projects (Ctrl+Shift+O) | done |  |
| `nextNeedsYou` | Next agent that needs you (Ctrl+Shift+U) | done |  |
| `managerBar` | Message the manager (Ctrl+Shift+H) | done |  |
| `help` | Help (F1) | done |  |
| `settings` | Settings (Ctrl+,) | done |  |
| `toggleRail` | Expand or collapse the rail (Ctrl+B) | done |  |
| `fontUp` | Increase font size (Ctrl+=) | done |  |
| `fontDown` | Decrease font size (Ctrl+-) | done |  |
| `fontReset` | Reset font size (Ctrl+0) | done |  |
| `remote` | Flockdeck Remote… | done |  |
| `helpers` | Helper apps… | done |  |
| `openRecordings` | Open recordings folder | done |  |
| `detach` | Detach — close the window, leave agents running | done |  |
| `quit` | Quit — stop every agent in every project | done |  |

## Commands the old interface sends

| Command | Status | |
| --- | --- | --- |
| `accentColor` | done |  |
| `addRepoToGroup` | done |  |
| `agents` | planned | T136 (L1, start flows and pane lifecycle). The agent picker |
| `approveBaton` | done |  |
| `archiveProject` | done |  |
| `autoReview` | done |  |
| `autoReviewDefault` | done |  |
| `browse` | done |  |
| `changes` | done |  |
| `checkForUpdate` | done |  |
| `clearRoutingLog` | done |  |
| `closeFinishedPanes` | planned | T136 (L1, start flows and pane lifecycle) |
| `closePane` | done |  |
| `closeProject` | done |  |
| `closeTab` | done |  |
| `commit` | done |  |
| `conflictRadar` | planned | T133 (C7, worktrees) |
| `conversations` | done |  |
| `cursorBlink` | done |  |
| `cursorStyle` | done |  |
| `detach` | done |  |
| `diff` | done |  |
| `dismissTip` | planned | T137 (L2, settings) |
| `exportTranscript` | done |  |
| `findPane` | done |  |
| `focusPane` | planned | T136 (L1, start flows and pane lifecycle) |
| `fontFamily` | done |  |
| `fontSize` | done |  |
| `ghChecks` | planned | T134 (C8, GitHub) |
| `ghInstall` | planned | T134 (C8, GitHub) |
| `ghIssue` | planned | T134 (C8, GitHub) |
| `ghIssueComment` | planned | T134 (C8, GitHub) |
| `ghIssueCreate` | planned | T134 (C8, GitHub) |
| `ghIssues` | planned | T134 (C8, GitHub) |
| `ghLogin` | planned | T134 (C8, GitHub) |
| `ghLoginCancel` | planned | T134 (C8, GitHub) |
| `ghLogout` | planned | T134 (C8, GitHub) |
| `ghPR` | planned | T134 (C8, GitHub) |
| `ghPRComment` | planned | T134 (C8, GitHub) |
| `ghPRCreate` | planned | T134 (C8, GitHub) |
| `ghPRs` | planned | T134 (C8, GitHub) |
| `ghStatus` | planned | T134 (C8, GitHub) |
| `gitFetch` | done |  |
| `gitPull` | done |  |
| `gitPush` | done |  |
| `groupProjects` | done |  |
| `helpSeen` | planned | T138 (L3, help). The first-run welcome |
| `helperInstall` | done |  |
| `helperOpen` | done |  |
| `helperPlan` | done |  |
| `helperStart` | done |  |
| `helperStop` | done |  |
| `helperUninstall` | done |  |
| `helpers` | done |  |
| `installVersion` | done |  |
| `jevKey` | done |  |
| `jevStatus` | done |  |
| `keyClear` | done |  |
| `keySet` | done |  |
| `keys` | done |  |
| `listVersions` | done |  |
| `lockPane` | done |  |
| `makeBaton` | done |  |
| `mergeAllTabs` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `mergeTab` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePane` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneDir` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneToNewTab` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `movePaneToTab` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `moveTab` | done |  |
| `msgList` | planned | T143 (L8, remaining actions). Team messages have no task |
| `msgSeen` | planned | T143 (L8, remaining actions). Team messages have no task |
| `msgSend` | planned | T143 (L8, remaining actions). Team messages have no task |
| `newTab` | done |  |
| `nextTab` | done |  |
| `notifications` | done |  |
| `openProject` | done |  |
| `openRecordings` | done |  |
| `paneInfo` | done |  |
| `presence` | done |  |
| `prevTab` | done |  |
| `pushAnonymous` | done |  |
| `pushDelay` | done |  |
| `pushNotify` | done |  |
| `quit` | done |  |
| `railExpanded` | planned | T137 (L2, settings) |
| `railWidth` | planned | T137 (L2, settings) |
| `recents` | done |  |
| `recordPane` | done |  |
| `refreshAgents` | planned | T136 (L1, start flows and pane lifecycle). The agent picker |
| `remoteDevices` | done |  |
| `remoteDisable` | done |  |
| `remoteEnable` | done |  |
| `remoteMove` | done |  |
| `remotePair` | done |  |
| `remoteReconnect` | done |  |
| `remoteRemove` | done |  |
| `remoteRename` | done |  |
| `remoteRevoke` | done |  |
| `remoteUnverify` | done |  |
| `remoteVerify` | done |  |
| `removeProject` | done |  |
| `removeRepoFromGroup` | done |  |
| `renameGroup` | done |  |
| `renameProject` | done |  |
| `renameTab` | done |  |
| `reorderProjects` | done |  |
| `resetKeybinding` | planned | T137 (L2, settings) |
| `resetKeybindings` | planned | T137 (L2, settings) |
| `resetTips` | done |  |
| `restart` | done |  |
| `restartPane` | done |  |
| `restartWithBaton` | done |  |
| `resumeConversation` | done |  |
| `revealPane` | done |  |
| `revealTranscript` | done |  |
| `save` | done |  |
| `saveBaton` | done |  |
| `screenReader` | done |  |
| `scrollback` | done |  |
| `selectProject` | done |  |
| `selectRepo` | done |  |
| `selectTab` | done |  |
| `sendPrompt` | replaced | sent as `paneSend`. Prompting one pane; prompting all is cut |
| `setAgentAddress` | planned | T137 (L2, settings) |
| `setAgentDefault` | planned | T137 (L2, settings) |
| `setDeviceRole` | done |  |
| `setKeybinding` | planned | T137 (L2, settings) |
| `setRouting` | done |  |
| `setWeights` | cut | Tiling and splitting panes is cut; the team is a graph. |
| `splitPane` | cut | Tiling and splitting panes is cut; the team is a graph. |
| `startAgent` | done |  |
| `startFromBaton` | done |  |
| `statusLine` | planned | T137 (L2, settings) |
| `swapPanes` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `taskAdd` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskAssign` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskBranch` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskGet` | done |  |
| `taskList` | planned | T131 (C5, task detail) |
| `taskNote` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskPrune` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskStatus` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `teamClose` | planned | T136 (L1, start flows and pane lifecycle) |
| `teamUnwatch` | replaced | sent as `graphUnwatch` |
| `teamWatch` | replaced | sent as `graphWatch` |
| `theme` | done |  |
| `tilePanes` | cut | Tiling and splitting panes is cut; the team is a graph. |
| `toggleBroadcast` | cut | Broadcasting to panes is cut; a manager sends to its team. |
| `toggleBroadcastMember` | cut | Broadcasting to panes is cut; a manager sends to its team. |
| `toggleZoom` | planned | T136 (L1, start flows and pane lifecycle) |
| `updates` | done |  |
| `worktreeAdd` | planned | T133 (C7, worktrees) |
| `worktreePrune` | planned | T133 (C7, worktrees) |
| `worktreeRemove` | planned | T133 (C7, worktrees) |
| `worktrees` | planned | T133 (C7, worktrees) |

## Commands only the new interface sends

`conversationClose`, `conversationDetail`, `conversationOlder`, `conversationOpen`, `conversationSearch`, `graphUnwatch`, `graphWatch`, `paneSend`, `permissionAnswer`, `structuredCancel`, `structuredUnwatch`, `structuredWatch`

## What this does not cover

- The pane sockets (`/ws/pty`, `/ws/artifacts`) and the pages the old interface fetches (`help.json` and the like). Only the control socket's `cmd` names are compared.
- A feature with no action or command of its own, such as a dialog that opens from a click on a title, or how a screen reader hears a row. This list says a command exists, not that its screen is as good.
- A command the old interface builds from a name it was sent. The places that do are listed in `classify.go` (`dynamicSites`) and the test fails when a new one appears.
- Whether a command classified as replaced does everything the old one did.
