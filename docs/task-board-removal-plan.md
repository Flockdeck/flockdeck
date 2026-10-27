# Task board removal plan

Plan for T203, written 27 Oct 2026 against origin/main `812a1767` (API 1.15). Decision 5 of [ACP direction](acp-direction.md) removes the task board once the new mail path works. This page lists what the board touches, the stages that remove it, and how to do that without breaking the programme that is using it now. Nothing is removed by this page.

Two things called "task" are not the board and stay: a pane's opening task (`Pane.Task`, `flockdeck spawn <task>`, `ContextTask`, the routing rules' `task` condition, the Task drawer in `ui/src/drawer/Task.js`) and Claude's background tasks (`TaskStop`, `<task-notification>`, `BackgroundTasks`). Every grep below was filtered for that. A worker must not delete either.

## What the board is

- Package `internal/tasks` (about 4,400 lines with tests): `Board` (file, lock, load, save), the task type and states (planned, assigned, in-progress, in-review, done, blocked, cancelled), the move rules by role (`rules.go`), the notices a change sends (`notify.go`), `Counts`, and `EnsureExcluded` (adds `.flockdeck/team/` to the repository's `info/exclude`).
- Data file: `<main checkout>/.flockdeck/team/tasks.json`, format version 1, one per project. A damaged file is moved aside as `tasks.json.corrupt-<ns>`. Nothing else writes under `.flockdeck/team/`, so after removal nothing does.
- Workspace side: `internal/workspace/tasks.go` (actor, board per project, `Task`, `TaskCounts`, `TaskOpening`, `GiveTaskToPane`), the `tasks taskState` field in `workspace.go`, `teamui.go` (`TeamBoard`, `UserTask`).
- Hook server: `internal/hooks/tasks.go` (`/task` route, `TaskRequest`, `TaskResult`, ops), `onTeam.task` in `hooks/team.go`, the route in `hooks.go:718`.
- CLI: `cli_task.go` and `main.go` (the `task` dispatch at 203, the allow-list entry at 388, usage lines at 502 to 505, and `spawn -task`).
- Server: `internal/server/team.go` (handler wiring), `teamui.go` (team message, `taskView`, ten `task*` commands, `teamTask`), `teamgraph.go` (task chips, `boardError`, per-team `taskId`, reviewer and task holds), `changes_team.go` (pane `taskId` from the board), `spawn.go` (`TaskOpening` at 671, `GiveTaskToPane` at 889), `viewerrole.go` and `control.go` (access table, command fields), `relayscrub.go` (the `tasks` message is desk-only).
- Derivation: `internal/team/graph.go` (`GraphTask`, `WaitReviewer`, `WaitTask`, `reviewerLabel`), `needs.go` (`NeedReturned`), `team.go:225`.
- Briefings: `internal/workspace/briefing.go` (`writeTasks`, `canManageTasks`), `context.go` (`TaskCounts`, `HasCounts`), `internal/agent/roles.go` (manager, planner, worker, reviewer, tester, docs), `promote.go:55`, and the idle notices in `team.go:513` and `structuredpane.go:601` that point at `flockdeck task list`.
- Interface (`ui/`): the Tasks tab of the Team sheet (`shell/Team.js`, `lib/team.js`), the Board view (`shell/Views.js`), the Dependencies view (`graph/DepsView.js`, `deps.js`, `deps.css`), task chips (`graph/chips.js`), `model.js` (`taskOfTeam`, `taskLabelOf`, `team.taskId`), the goal line, the feed diff (`FeedDiff.js`), the decision block's reassign button, the "returned" badge, `changes/ChangesList.js` and `lib/changesteam.js` (`taskId`), `lib/store.js`, `shell/Live.js`, `keys.json`, `help.json`.

## Every reference, from `git grep` on origin/main

Delete whole files (no other content):

- `internal/tasks/` (all 17 files), `cli_task.go`, `cli_task_test.go`, `internal/hooks/tasks.go`, `internal/workspace/tasks.go`, `internal/workspace/tasks_test.go`
- `internal/server/tasks_test.go`, `taskteam_test.go`, `teamdepends_test.go` (`panetask_test.go` is about the pane's opening task: keep it)
- `ui/src/graph/DepsView.js`, `deps.js`, `deps.css`, `ui/test/deps.test.mjs`, `deps-strength.test.mjs`, `depsview.test.mjs`, `tree-dep-chips.test.mjs`
- `.flockdeck/knowledge/cli/task.md`, `data/task.md`, `data/task-board.md`, `gotchas/task-board-file-recovery.md`, `packages/tasks.md`, `packages/workspace-tasks.md`, `security/task-board-permissions.md`, `api/hooks-task.md`

Edit (board part only):

| Area | Files |
| --- | --- |
| Go, hooks | `internal/hooks/hooks.go` (route 718; `SpawnRequest.TaskID` 999, `SpawnResult.TaskID` and `TaskError` 1057 to 1061), `hooks/team.go`, `hooks_test.go` |
| Go, workspace | `workspace.go` (field 552), `context.go`, `briefing.go`, `promote.go`, `team.go`, `structuredpane.go`, `teamui.go`, `teamui_test.go`, `roles_test.go`, `contextsize_test.go`, `background_check.go` and its test (check: mostly Claude background tasks) |
| Go, server | `team.go`, `teamui.go`, `teamgraph.go`, `changes_team.go`, `spawn.go`, `viewerrole.go`, `control.go`, `relayscrub.go`; tests `teamui_test.go`, `teamgraph_*_test.go`, `teamlive_test.go`, `relayteams_test.go`, `relaydeny_test.go`, `relaytext_test.go`, `api_fixtures_test.go`, `graph_fixtures_test.go`, `spawn_default_worktree_test.go` |
| Go, other | `internal/team/graph.go`, `graph_test.go`, `needs.go`, `needs_test.go`, `team.go`; `internal/changes/team.go`, `types.go`; `internal/agent/roles.go`; `internal/review/mail_test.go`; `main.go`, `main_test.go`, `cli_spawntask_test.go` |
| API | `api/catalog.json` (11 `task*` commands, `tasks` message, version), `api/schema/team-graph.schema.json`, `changes.schema.json`, `api/fixtures/*` (five recorded graphs and `built-changes-01-team-mixed.json`), `api/deprecated.md`, `internal/server/apiversion.go` |
| UI | `shell/Team.js`, `shell/Views.js`, `shell/Live.js`, `lib/team.js`, `lib/store.js`, `lib/changesteam.js`, `graph/chips.js`, `model.js`, `GoalLine.js`, `FeedDiff.js`, `Badge.js`, `DecisionBlock.js`, `changes/ChangesList.js`, `keys.json`, `help.json`; tests `team.test.mjs`, `store.test.mjs`, `chips.test.mjs`, `edge-data.test.mjs`, `goal-tabs.test.mjs`, `goalline.test.mjs`, `model-strength.test.mjs`, `contentmenus.test.mjs`, `changes-team*.test.mjs`, `store-changes.test.mjs`; `scripts/realpage/check.mjs` (the "returned" strings) |
| Help | `ui/help/keys.go:111`, `ui/help/pages/agents.md` (section "The task board", the manager walk-through at 327 to 350, the Tasks tab at 433 to 459), `ui/help/pages/cli.md` (18 mentions) |
| Docs | `docs/agent-briefing.md` (section "The task board"), `architecture.md`, `changes-api.md`, `team-graph.md`, `keys.md`, `parity.md`, `status-matrix.md`, `acp-direction.md`, `claude-headless-spike.md`; `README.md`; `cmd/parity/oldcommands.txt` |
| Release notes | `docs/releases/v0.3.47.md`, `v0.6.0.md`, `v0.7.0.md` are history: leave them |
| Knowledge | `.flockdeck/knowledge/` `index.md` files for api, cli, data, gotchas, packages, security; `packages/workspace.md`, `workspace-briefing.md`, `workspace-team-ui.md`, `hooks.md`, `team.md`, `data/team-graph.md`, `api/ws-commands-agents-team.md` |
| CI | `.github/test-weights.txt:59` (drop the `internal/tasks` line) |

Check before editing, each could be a false match: `internal/hooks/notification.go` (background task ids, keep), `cmd/release/main.go` and `winres` (Windows taskbar), `internal/session/background.go` (keep), `internal/route` (keep).

One dependency to unpick: `tasks.UserActor`, `tasks.HasManager`, `tasks.ParseID` and `tasks.UserName` are used outside board code (`server/teamui.go` for `hasManager` on the team message, `workspace/teamui.go`). `HasManager(members)` is the only one the interface still needs; move it to `internal/team` in stage 2 before the package goes.

## What briefings say instead

Mail: `send_message` and `list_peers` from the Flockdeck MCP server in structured panes. A pane with no MCP server (a terminal pane, until decision 1 ends them) keeps `flockdeck msg send` and `flockdeck inbox` for now; those go in their own removal, not this one.

- Manager: breaks the request into steps and keeps its own list in its conversation and notes. It puts the whole assignment in the helper's spawn prompt (title, details, branch, who to report to, how to report) and no longer runs `task add`, `assign` or `status`. It learns progress from helper reports (`send_message` to the manager) and from `flockdeck team`, which shows panes, branches, activity and mail. It decides done itself when a reviewer reports a good change.
- Planner: returns the plan as its final report (a message to the manager), one block per step with title, details and role. No `task add`.
- Worker: starts on the prompt it was given. Reports with `send_message` when the change is committed and tested ("ready for review: branch, what changed, tests run"), when blocked ("blocked: why"), and when sent back.
- Reviewer and tester: the worker's report and the spawn prompt name the branch. They report a verdict to whoever started them (and to the manager) with `send_message`. Sending work back is a message to the worker.
- Docs: as a worker.
- Spawn prompt text built by `TaskOpening` (`flockdeck task show`, `task status in-progress`) goes with `spawn -task`. Nothing replaces it: the prompt is the spawner's text.
- `promote.go` notice: drop the `task list` pointer. Idle notices (`team.go:513`, `structuredpane.go:601`): "its report is in `flockdeck inbox` if it sent one" only; for a structured parent, the report arrives as a prompt, so drop the pointer there too.
- Delete the "Task board" section of the briefing and the counts snapshot. Remove the matching test expectations and update `docs/agent-briefing.md`.

The manager's own tracking is the manager's job and a plain list in its conversation is enough. Flockdeck gives it no new store. If the owner wants one later, that is a new feature with its own task; do not rebuild the board under another name.

## Stored data and old snapshots

- `tasks.json` and `tasks.json.corrupt-*`: leave them on disk. Do not read, migrate or delete them. They are the user's files in the user's repository, ignored through `info/exclude`. Document in the release notes that the file is no longer read and may be deleted by hand. The `info/exclude` line stays; removing it could surface the old file as an untracked file in `git status`.
- Before the CLI goes, the manager should export its board once (`flockdeck task list -state all -json`) into its own notes. That is the only copy of closed task history.
- Saved layouts and panes hold no board state. `Pane.Task` is the opening task, not a board id. Nothing in `store` references the board.
- Recorded graph fixtures under `api/fixtures/` carry `task` chips and `returned` items, and `api_fixtures_test.go` builds a board to make them. Rewrite the scenarios without tasks, regenerate, and rename the two files with "returned-task" in the name. Treat the old files as history in git, not as inputs.
- A relay or phone client older than the change: it already ignores `team`, `tasks` and `graph` (checked at flockdeck-remote `b94f37b`, see `api/deprecated.md`). Its team and graph messages are desk-only.

## API and catalog

- Removed from `api/catalog.json`: commands `taskAdd`, `taskAssign`, `taskBranch`, `taskDepends`, `taskEstimate`, `taskGet`, `taskList`, `taskNote`, `taskPrune`, `taskStatus`, `taskTeam`; message `tasks`. Changed: message `team` (drops `tasks`, `counts` and `boardError`), message `graph` (drops `nodes[].task`, `teams[].taskId`, `boardError`, the `reviewer` and `task` hold kinds, the `returned` need kind and the item field `task`).
- Schemas: `team-graph.schema.json` loses `taskChip`, `boardError`, `taskId`, the two enum values, `returned` and the item `task`; `changes.schema.json` loses `taskId` on a pane. Update `docs/team-graph.md`, `docs/changes-api.md`, and `deprecatedFields` if it lists any of these (it does not today).
- Version: this is a removal, which the rule in `apiversion.go` and `api/README.md` calls a major bump. The 1.15 removal (`setAgentAddress`) set the precedent for a minor bump when only the desktop interface used the removed items, and said so in `api/deprecated.md`. The same holds here for the desktop interface and flockdeck-remote. It has not been checked for `flockdeck-relay`, `flockdeck-site` or `flockdeck-billing`, which `deprecated.md` says were not searched. Recommended: one minor bump, taken at merge as the next free minor after T207's 1.16, with a "Removed in 1.x" paragraph in `api/deprecated.md`. A server answers the removed commands as `unsupported`. Check the three private repositories before the merge; if any reads these items, raise the major instead and tell the owner. Do not split the removal over several minors: one stage owns all catalog and schema changes (stage 3).
- `apiCaps` has no board cap, so nothing to remove there. The hello feature list is unchanged.
- The hook server's `/task` route and `SpawnRequest.TaskID` are not WebSocket API; they change with the CLI (stage 4) and need no catalog entry.

## Stages

Each stage builds and passes the tests of the packages it touches. Order matters: a stage never removes something a later stage's code still calls. Stages 1 and 5 touch no code a running agent depends on.

1. Stop teaching the board. Rewrite roles and the briefing (see above), remove `writeTasks`, `TaskCounts` and `canManageTasks`, the notices that mention `task list`, the `TaskOpening` prompt's board lines, and update `docs/agent-briefing.md` and the agents help page text that the briefing mirrors. The board and `flockdeck task` still work, so agents already running keep working and any pane that restarts gets a briefing with no board. Gate: the owner agrees the manager and helpers run on messages and the manager's own list (see Sequencing). Tests: `internal/agent`, `internal/workspace` (roles, context size, briefing), root package.
2. Interface stops reading the board. Remove the Tasks tab (the sheet opens on Messages), the Board view, the Dependencies view and its keys and help entries, task chips, the goal-line task, feed diffs of tasks, the reassign button, the "returned" badge and `taskId` on the changes list. Move `HasManager` into `internal/team`. The server still sends everything, so no API change. Tests: the `ui` node tests and `scripts/realpage/check.mjs`; no Go package changes except `internal/team`.
3. Server stops serving the board (API bump). Delete the ten commands and `teamTask`, the `tasks` message, the board part of `teamMsg`, chips, `boardError`, per-team `taskId`, the holds and `returned` needs (`internal/team`), `changes_team` `taskId`, the access table entries, the relay deskOnly entry, `GraphTask` and its callers. Update catalog, schemas, fixtures (regenerate), `docs/team-graph.md`, `docs/changes-api.md`, `deprecated.md`, version constants and the version test. The workspace still has the board, but the server no longer reads it. Tests: `internal/team`, `internal/changes`, `internal/server` (slow, once), root `api` tests if any.
4. Remove the board itself. Delete `internal/tasks`, `workspace/tasks.go` and `taskState`, `TeamBoard` and `UserTask`, `hooks/tasks.go`, the `/task` route and `onTeam.task`, `cli_task.go`, the `main.go` dispatch, allow-list and usage lines, `spawn -task`, `SpawnRequest.TaskID`, `SpawnResult.TaskID` and `TaskError`, `TaskOpening` and `GiveTaskToPane` and their calls in `spawn.go`, and the test-weights line. Run `go vet` to catch dead imports. `flockdeck task` then reaches the normal unknown-command path; no stub, because stage 1 had already taught the programme to stop using it. Tests: `internal/hooks`, `internal/workspace` (slow, once), `internal/server` (slow, once, after stage 3's run is finished), root package.
5. Docs and knowledge. Remove the board sections from `ui/help/pages/agents.md` and `cli.md`, `README.md`, `architecture.md`, `parity.md`, `status-matrix.md`, `keys.md`, `cmd/parity/oldcommands.txt`; delete the eight knowledge notes and fix the index files and cross-links; mark decision 5 and the "Do not build on these" line in `acp-direction.md` as done; one line in the next release notes (the board is gone, `tasks.json` is no longer read and can be deleted). Tests: knowledge note checks (`cli_knowledge_test.go`), the help page tests in `ui/help`, root package.

Stages 2 and 5 can run beside others if workers keep to their files. Stage 3 needs 2 merged, stage 4 needs 3 merged. Stage 1 can start at once but should merge last of the early ones (see below).

## Risks and sequencing

- The programme running now assigns work through `flockdeck task` (this task is T203, the manager's briefing and every helper's brief name board ids). A removal that lands while those panes are alive breaks their commands. The briefing text of a running agent does not change, only a restart picks up stage 1. Therefore: merge stages 2 and 3 first only if the manager is not using the Team sheet's Tasks tab and the graph chips to track work (they are display only; the CLI keeps working); merge stage 1 when the manager has moved its tracking to its own list and every new brief carries the full assignment; merge stage 4 only when no live pane was briefed with the board. Practical test: `flockdeck team -json` shows no open task in `assigned`, `in-progress` or `in-review`, and the manager has exported the board.
- If the manager restarts after stage 1 and before it has its own list, it forgets the plan. The manager should keep its list in its memory directory, not only in the conversation.
- Old helpers using `flockdeck task` after stage 4 get an error. Their reports still arrive by mail; nothing is lost but status moves.
- Reviewer wake-ups: today `in-review` tells the reviewer. After stage 1 only a message does. The worker brief must name the reviewer's pane id or the manager must forward it; otherwise nobody is told. Add a test that the worker briefing tells it to report with `send_message` and who to.
- The graph's "waiting on reviewer" and "returned" needs disappear with the tasks. A worker waiting for review shows as idle with mail, not as held. Check the needs strip and the feed after stage 3 against `scripts/realpage/check.mjs`.
- Rebase friction: stage 3 and T207 both change the catalog and `apiversion.go`. Take the next free minor at rebase and re-run the version test; do not keep a number from this page.
- Windows: no path or case issues expected. Deleting `ui/src/graph/deps.js` and a differently cased file of another name would be the only case risk; list each directory before writing.
- Hostile text: stage 1 moves task text from a board with permissions to prompts. A report from one agent reaches another through the fixed sender prefix of mail, so the briefing must keep telling agents that mail is not a user instruction. Keep that sentence.
- Memory and tests: `internal/workspace` and `internal/server` runs are heavy. Stages 3 and 4 each need one run of each; check `tasklist | grep -ci '\.test'` first and run them one at a time.
- Tests must fail without the code: after removing a board test, the matching assertion (a briefing without the "Task board" heading, a catalog without the commands, an unknown `taskList` answered `unsupported`) must exist and be shown to fail against the old code.

## Worker tasks

| # | Task | Stage | Size | Needs |
| --- | --- | --- | --- | --- |
| W1 | Rewrite roles, briefing, notices and `agent-briefing.md` for mail and manager tracking; delete `writeTasks`, `TaskCounts`; update tests | 1 | M | owner go-ahead |
| W2 | Interface: remove Tasks tab, Board view, Dependencies view, chips, goal line, feed diffs, badge, reassign, `taskId`; keys, help, node tests, realpage check | 2 | L | none |
| W3 | Move `HasManager` to `internal/team`; remove board use from `internal/team` graph and needs | 3 | S | W2 |
| W4 | Server: delete commands, messages, snapshot and graph fields, changes `taskId`, access and relay tables | 3 | L | W2, W3 |
| W5 | API: catalog, schemas, fixtures regenerated, `deprecated.md`, version bump, `team-graph.md`, `changes-api.md`; check the three private repos | 3 | M | with W4 |
| W6 | Delete `internal/tasks`, workspace board code, `/task` route, CLI, `spawn -task` and result fields, test weights | 4 | M | W4 merged, W1 merged |
| W7 | Docs: help pages, README, architecture, parity, status matrix, keys, oldcommands, `acp-direction.md` | 5 | S | W6 |
| W8 | Knowledge notes: delete eight, fix indexes and links, add a gotcha line about the left-behind `tasks.json` | 5 | S | W6 |
| W9 | Full check: grep for `flockdeck task`, `tasks.json`, `internal/tasks`, `taskAdd`; run slow packages once, one at a time | after 6 | S | W6 to W8 |

W4 and W5 are one pull request. W2 and W1 can be done by separate workers at once. The programme gate is the manager's, not a worker's: no worker merges W1 or W6.
