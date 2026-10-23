# Parity of the new interface with the old

This file is written by `go run ./cmd/parity -w`. Do not edit it: change `cmd/parity/classify.go` or the interface and write it again. A test in `cmd/parity` fails when this file is out of date, and when the old interface does something that is neither done, replaced, cut nor owned by a task.

It compares two lists the sources hold: the actions of the key table (`internal/help/keys.go`) against what the new shell runs, and the control-socket commands the old interface sends (`internal/webui/assets/app.js`) against the ones the new interface sends. It does not look at the pane sockets or the pages the old interface fetches, and it cannot see an old feature that has no action or command of its own. See [what this does not cover](#what-this-does-not-cover).

## Summary

| | Actions | Commands |
| --- | --- | --- |
| done | 24 | 21 |
| replaced | 0 | 3 |
| cut | 14 | 11 |
| planned | 20 | 121 |
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
| command | `splitPane` | Tiling and splitting panes is cut; the team is a graph. |
| command | `swapPanes` | Moving and merging panes and tabs is cut; the team is a graph. |
| command | `tilePanes` | Tiling and splitting panes is cut; the team is a graph. |
| command | `toggleBroadcast` | Broadcasting to panes is cut; a manager sends to its team. |
| command | `toggleBroadcastMember` | Broadcasting to panes is cut; a manager sends to its team. |

## Planned, by task

| Task | Actions | Commands |
| --- | --- | --- |
| T131 (C5, task detail) |  | `taskGet`, `taskList` |
| T132 (C6, Changes) | `changes` | `changes`, `commit`, `diff`, `gitFetch`, `gitPull`, `gitPush` |
| T133 (C7, worktrees) | `worktrees` | `conflictRadar`, `worktreeAdd`, `worktreePrune`, `worktreeRemove`, `worktrees` |
| T134 (C8, GitHub) |  | `ghChecks`, `ghInstall`, `ghIssue`, `ghIssueComment`, `ghIssueCreate`, `ghIssues`, `ghLogin`, `ghLoginCancel`, `ghLogout`, `ghPR`, `ghPRComment`, `ghPRCreate`, `ghPRs`, `ghStatus` |
| T136 (L1, start flows and pane lifecycle) | `selectTab`, `closeFinishedPanes` | `agents`, `browse`, `closeFinishedPanes`, `focusPane`, `recents`, `refreshAgents`, `teamClose`, `toggleZoom` |
| T137 (L2, settings) |  | `dismissTip`, `railExpanded`, `railWidth`, `resetKeybinding`, `resetKeybindings`, `setAgentAddress`, `setAgentDefault`, `setKeybinding`, `statusLine` |
| T138 (L3, help) |  | `helpSeen` |
| T139 (L4, history, find, pane info) | `recordPane`, `exportTranscript`, `revealTranscript`, `paneInfo`, `findInTerminal`, `findPane`, `history`, `openRecordings` | `conversations`, `exportTranscript`, `findPane`, `openRecordings`, `paneInfo`, `recordPane`, `resumeConversation`, `revealPane`, `revealTranscript` |
| T140 (L5, API keys, remote, notifications) | `apiKeys`, `remote` | `keyClear`, `keySet`, `keys`, `notifications`, `presence`, `pushAnonymous`, `pushDelay`, `pushNotify`, `remoteDevices`, `remoteDisable`, `remoteEnable`, `remoteMove`, `remotePair`, `remoteReconnect`, `remoteRemove`, `remoteRename`, `remoteRevoke`, `remoteUnverify`, `remoteVerify`, `setDeviceRole` |
| T141 (L6, helpers, baton, projects) | `makeBaton`, `helpers` | `addRepoToGroup`, `approveBaton`, `archiveProject`, `closeProject`, `groupProjects`, `helperInstall`, `helperOpen`, `helperPlan`, `helperStart`, `helperStop`, `helperUninstall`, `helpers`, `makeBaton`, `openProject`, `removeProject`, `removeRepoFromGroup`, `renameGroup`, `renameProject`, `reorderProjects`, `restartWithBaton`, `saveBaton`, `selectProject`, `selectRepo`, `startFromBaton` |
| T142 (L7, updates, detach, quit) | `detach`, `quit` | `checkForUpdate`, `detach`, `installVersion`, `listVersions`, `quit`, `restart`, `save`, `updates` |
| T143 (L8, remaining actions) | `agents`, `team` | `autoReview`, `clearRoutingLog`, `jevKey`, `jevStatus`, `msgList`, `msgSeen`, `msgSend`, `setRouting`, `setWeights`, `taskAdd`, `taskAssign`, `taskBranch`, `taskNote`, `taskPrune`, `taskStatus` |

## Must exist before the old interface is retired

These are safety and security controls. The new interface has no way to do them yet, and T162 (the flip) must not remove the old interface until it does.

| Command | Owner | Gate | What it is |
| --- | --- | --- | --- |
| `autoReview` | T143 (L8, remaining actions) | T162 | Safety control: the new shell cannot turn auto review off |
| `jevKey` | T143 (L8, remaining actions) | T162 | Security control: the Jev key is set and cleared here |

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
| `recordPane` | Start recording | planned | T139 (L4, history, find, pane info). Recording a terminal pane; recording a structured pane is T145 (P1) |
| `exportTranscript` | Export transcript | planned | T139 (L4, history, find, pane info) |
| `revealTranscript` | Reveal transcript | planned | T139 (L4, history, find, pane info) |
| `paneInfo` | Pane info (Ctrl+Shift+Y) | planned | T139 (L4, history, find, pane info) |
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
| `makeBaton` | Make baton: hand this pane's work to another agent | planned | T141 (L6, helpers, baton, projects) |
| `agents` | All agents across projects (Ctrl+Shift+A) | planned | T143 (L8, remaining actions). The list of agents across projects has no task; decide whether the graph's project switch is enough |
| `closeFinishedPanes` | Close finished panes — idle agents and cleanly exited panes, in every open project | planned | T136 (L1, start flows and pane lifecycle) |
| `startTeam` | Start a team — a manager agent that starts the rest | done |  |
| `team` | Team — this project's roster, task board and messages (Ctrl+Shift+M) | planned | T143 (L8, remaining actions). The graph is the roster; the task board and messages dialog has no task |
| `apiKeys` | API keys… | planned | T140 (L5, API keys, remote, notifications) |
| `worktrees` | Worktrees (Ctrl+Shift+G) | planned | T133 (C7, worktrees) |
| `changes` | Review changes, commit and push (Ctrl+Shift+S) | planned | T132 (C6, Changes) |
| `palette` | Command palette (Ctrl+Shift+K) | done |  |
| `nextRegion` | Move to the next part of the window (F6) | done |  |
| `prevRegion` | Move to the previous part of the window (Shift+F6) | done |  |
| `findInTerminal` | Find in terminal (Ctrl+Shift+F) | planned | T139 (L4, history, find, pane info) |
| `findPane` | Find pane (Ctrl+Shift+L) | planned | T139 (L4, history, find, pane info) |
| `history` | Resume — reopen a past conversation (Ctrl+Shift+R) | planned | T139 (L4, history, find, pane info) |
| `projects` | Projects (Ctrl+Shift+O) | done |  |
| `nextNeedsYou` | Next agent that needs you (Ctrl+Shift+U) | done |  |
| `managerBar` | Message the manager (Ctrl+Shift+H) | done |  |
| `help` | Help (F1) | done |  |
| `settings` | Settings (Ctrl+,) | done |  |
| `toggleRail` | Expand or collapse the rail (Ctrl+B) | done |  |
| `fontUp` | Increase font size (Ctrl+=) | done |  |
| `fontDown` | Decrease font size (Ctrl+-) | done |  |
| `fontReset` | Reset font size (Ctrl+0) | done |  |
| `remote` | Flockdeck Remote… | planned | T140 (L5, API keys, remote, notifications) |
| `helpers` | Helper apps… | planned | T141 (L6, helpers, baton, projects) |
| `openRecordings` | Open recordings folder | planned | T139 (L4, history, find, pane info) |
| `detach` | Detach — close the window, leave agents running | planned | T142 (L7, updates, detach, quit) |
| `quit` | Quit — stop every agent in every project | planned | T142 (L7, updates, detach, quit) |

## Commands the old interface sends

| Command | Status | |
| --- | --- | --- |
| `accentColor` | done |  |
| `addRepoToGroup` | planned | T141 (L6, helpers, baton, projects) |
| `agents` | planned | T136 (L1, start flows and pane lifecycle). The agent picker |
| `approveBaton` | planned | T141 (L6, helpers, baton, projects). Arrives from the desktop in a notice's action, not named in the old interface |
| `archiveProject` | planned | T141 (L6, helpers, baton, projects) |
| `autoReview` | planned | T143 (L8, remaining actions). Safety control: the new shell cannot turn auto review off |
| `autoReviewDefault` | done |  |
| `browse` | planned | T136 (L1, start flows and pane lifecycle). The folder picker of the start flow |
| `changes` | planned | T132 (C6, Changes) |
| `checkForUpdate` | planned | T142 (L7, updates, detach, quit) |
| `clearRoutingLog` | planned | T143 (L8, remaining actions). Routing has no task |
| `closeFinishedPanes` | planned | T136 (L1, start flows and pane lifecycle) |
| `closePane` | done |  |
| `closeProject` | planned | T141 (L6, helpers, baton, projects) |
| `closeTab` | done |  |
| `commit` | planned | T132 (C6, Changes) |
| `conflictRadar` | planned | T133 (C7, worktrees) |
| `conversations` | planned | T139 (L4, history, find, pane info) |
| `cursorBlink` | done |  |
| `cursorStyle` | done |  |
| `detach` | planned | T142 (L7, updates, detach, quit) |
| `diff` | planned | T132 (C6, Changes) |
| `dismissTip` | planned | T137 (L2, settings) |
| `exportTranscript` | planned | T139 (L4, history, find, pane info) |
| `findPane` | planned | T139 (L4, history, find, pane info) |
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
| `gitFetch` | planned | T132 (C6, Changes) |
| `gitPull` | planned | T132 (C6, Changes) |
| `gitPush` | planned | T132 (C6, Changes) |
| `groupProjects` | planned | T141 (L6, helpers, baton, projects) |
| `helpSeen` | planned | T138 (L3, help). The first-run welcome |
| `helperInstall` | planned | T141 (L6, helpers, baton, projects) |
| `helperOpen` | planned | T141 (L6, helpers, baton, projects) |
| `helperPlan` | planned | T141 (L6, helpers, baton, projects) |
| `helperStart` | planned | T141 (L6, helpers, baton, projects) |
| `helperStop` | planned | T141 (L6, helpers, baton, projects) |
| `helperUninstall` | planned | T141 (L6, helpers, baton, projects) |
| `helpers` | planned | T141 (L6, helpers, baton, projects) |
| `installVersion` | planned | T142 (L7, updates, detach, quit) |
| `jevKey` | planned | T143 (L8, remaining actions). Security control: the Jev key is set and cleared here |
| `jevStatus` | planned | T143 (L8, remaining actions). The Jev status has no task |
| `keyClear` | planned | T140 (L5, API keys, remote, notifications) |
| `keySet` | planned | T140 (L5, API keys, remote, notifications) |
| `keys` | planned | T140 (L5, API keys, remote, notifications). The API keys list |
| `listVersions` | planned | T142 (L7, updates, detach, quit) |
| `lockPane` | done |  |
| `makeBaton` | planned | T141 (L6, helpers, baton, projects) |
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
| `notifications` | planned | T140 (L5, API keys, remote, notifications) |
| `openProject` | planned | T141 (L6, helpers, baton, projects) |
| `openRecordings` | planned | T139 (L4, history, find, pane info) |
| `paneInfo` | planned | T139 (L4, history, find, pane info) |
| `presence` | planned | T140 (L5, API keys, remote, notifications). Tells the desktop this window is in use, which notifications depend on |
| `prevTab` | done |  |
| `pushAnonymous` | planned | T140 (L5, API keys, remote, notifications) |
| `pushDelay` | planned | T140 (L5, API keys, remote, notifications) |
| `pushNotify` | planned | T140 (L5, API keys, remote, notifications) |
| `quit` | planned | T142 (L7, updates, detach, quit) |
| `railExpanded` | planned | T137 (L2, settings) |
| `railWidth` | planned | T137 (L2, settings) |
| `recents` | planned | T136 (L1, start flows and pane lifecycle). Recent folders of the start flow |
| `recordPane` | planned | T139 (L4, history, find, pane info) |
| `refreshAgents` | planned | T136 (L1, start flows and pane lifecycle). The agent picker |
| `remoteDevices` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteDisable` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteEnable` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteMove` | planned | T140 (L5, API keys, remote, notifications) |
| `remotePair` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteReconnect` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteRemove` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteRename` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteRevoke` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteUnverify` | planned | T140 (L5, API keys, remote, notifications) |
| `remoteVerify` | planned | T140 (L5, API keys, remote, notifications) |
| `removeProject` | planned | T141 (L6, helpers, baton, projects) |
| `removeRepoFromGroup` | planned | T141 (L6, helpers, baton, projects) |
| `renameGroup` | planned | T141 (L6, helpers, baton, projects) |
| `renameProject` | planned | T141 (L6, helpers, baton, projects) |
| `renameTab` | done |  |
| `reorderProjects` | planned | T141 (L6, helpers, baton, projects) |
| `resetKeybinding` | planned | T137 (L2, settings) |
| `resetKeybindings` | planned | T137 (L2, settings) |
| `resetTips` | done |  |
| `restart` | planned | T142 (L7, updates, detach, quit). Restart after an update |
| `restartPane` | done |  |
| `restartWithBaton` | planned | T141 (L6, helpers, baton, projects). Sent through a helper in the old interface |
| `resumeConversation` | planned | T139 (L4, history, find, pane info) |
| `revealPane` | planned | T139 (L4, history, find, pane info) |
| `revealTranscript` | planned | T139 (L4, history, find, pane info) |
| `save` | planned | T142 (L7, updates, detach, quit). Saves the layout when the page unloads; may be moot once tiling is cut |
| `saveBaton` | planned | T141 (L6, helpers, baton, projects). Sent through a helper in the old interface |
| `screenReader` | done |  |
| `scrollback` | done |  |
| `selectProject` | planned | T141 (L6, helpers, baton, projects). Tells the desktop which project is active |
| `selectRepo` | planned | T141 (L6, helpers, baton, projects) |
| `selectTab` | done |  |
| `sendPrompt` | replaced | sent as `paneSend`. Prompting one pane; prompting all is cut |
| `setAgentAddress` | planned | T137 (L2, settings) |
| `setAgentDefault` | planned | T137 (L2, settings) |
| `setDeviceRole` | planned | T140 (L5, API keys, remote, notifications) |
| `setKeybinding` | planned | T137 (L2, settings) |
| `setRouting` | planned | T143 (L8, remaining actions). Routing has no task |
| `setWeights` | planned | T143 (L8, remaining actions). Routing has no task |
| `splitPane` | cut | Tiling and splitting panes is cut; the team is a graph. |
| `startAgent` | done |  |
| `startFromBaton` | planned | T141 (L6, helpers, baton, projects). Sent through a helper in the old interface |
| `statusLine` | planned | T137 (L2, settings) |
| `swapPanes` | cut | Moving and merging panes and tabs is cut; the team is a graph. |
| `taskAdd` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskAssign` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskBranch` | planned | T143 (L8, remaining actions). Editing the task board has no task |
| `taskGet` | planned | T131 (C5, task detail) |
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
| `updates` | planned | T142 (L7, updates, detach, quit) |
| `worktreeAdd` | planned | T133 (C7, worktrees) |
| `worktreePrune` | planned | T133 (C7, worktrees) |
| `worktreeRemove` | planned | T133 (C7, worktrees) |
| `worktrees` | planned | T133 (C7, worktrees) |

## Commands only the new interface sends

`conversationClose`, `conversationOlder`, `conversationOpen`, `graphUnwatch`, `graphWatch`, `paneSend`, `permissionAnswer`, `structuredCancel`, `structuredUnwatch`, `structuredWatch`

## What this does not cover

- The pane sockets (`/ws/pty`, `/ws/artifacts`) and the pages the old interface fetches (`help.json` and the like). Only the control socket's `cmd` names are compared.
- A feature with no action or command of its own, such as a dialog that opens from a click on a title, or how a screen reader hears a row. This list says a command exists, not that its screen is as good.
- A command the old interface builds from a name it was sent. The places that do are listed in `classify.go` (`dynamicSites`) and the test fails when a new one appears.
- Whether a command classified as replaced does everything the old one did.
