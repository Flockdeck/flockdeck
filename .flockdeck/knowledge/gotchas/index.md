# Known gotchas

* [agents.json read and write rules](agents-json-write-rules.md) - Why agents.json is read raw, locked, atomically replaced, and never overwritten when unreadable.
* [appwindow build tags: cgo or Windows](appwindow-build-tags.md) - The native window is compiled only with cgo or on Windows; other builds get a stub whose Open always fails.
* [Embedded assets are served no-store](assets-are-never-cached.md) - index.html, assets and help.json send no-store so an old app.js cannot talk to an upgraded server.
* [Atomic state writes and Windows file contention](atomic-state-writes-and-windows-contention.md) - State files are written temp-then-rename with retries because Windows refuses a rename onto a file another process holds.
* [Background work counts only on evidence](background-work-verification.md) - An idle agent with background tasks reads as working only while the work is verified, and ends only on stored evidence.
* [A baton must fit a Windows command line](baton-prompt-size-budget.md) - The baton prompt is a command-line argument, so its size is budgeted below the 32,767-character Windows cap.
* [Unix socket path length and placement](channel-socket-path-length.md) - sun_path limits and logout behaviour decide where the local channel socket may live.
* [Claude session variables are stripped from panes](claude-env-stripping.md) - Variables a running Claude Code session injects are removed so a pane does not think it is a nested child.
* [Conflict radar throttles and fail-safes](conflict-radar-limits.md) - When the workspace runs the conflict radar, and how it backs off on slow or vanished checkouts.
* [The token cookie is named per port](cookie-named-per-port.md) - Cookies ignore ports, so each instance names its cookie flockdeck_token_<port>.
* [Concurrent git status and diff can fail on the index on Windows](git-index-gate.md) - gitx serialises index-rewriting git commands with a gate and reader/writer wait limits.
* [A /health that is not ready still means the instance is alive](health-not-ready-is-alive.md) - A busy workspace answers /health with 503 and ready=false; callers must wait, not start a second instance.
* [A control socket gets hello before any state](hello-before-state.md) - A window is sent no snapshot until its hello is queued; the first state replaces any older pending one.
* [Late hook events after a pane restart](hook-launch-token.md) - A restarted pane keeps its id, so each process start carries a launch marker to tell old hooks from new ones.
* [Idle detection differs by platform and may give no answer](idle-probe-fallbacks.md) - Linux and macOS read idle time by running a short command, and any failure yields ok=false.
* [Layout save and restore edge cases](layout-restore-rules.md) - Rules in persist.go that keep a restart from losing, duplicating or mis-sizing tabs and projects.
* [Mail notices are never typed into a pane](notices-not-typed.md) - Messages reach an agent in the reply to its next hook, because typing into the input box leaves text the user did not write.
* [Closing a pane must end its whole process tree](pane-process-tree-shutdown.md) - Killing only the pane's process leaves children running and holding the worktree open, so each OS uses a session or job object.
* [Prompt token expansion and the -- guard](prompt-token-expansion.md) - How BuildArgv expands tokens in one pass, drops value-less groups and guards prompts that start with a dash.
* [Terminal socket resume, restart and slow viewers](pty-resume-and-restart.md) - How /ws/pty survives pane restarts, drops slow viewers and resumes by offset.
* [Radar conflicts appear after two refreshes](radar-confirmation-delay.md) - The conflict radar waits for repeated sightings and rate limits merges, so a conflict is not shown the instant it exists.
* [Remote windows neither keep the app alive nor end it](remote-windows-do-not-keep-app-alive.md) - Only windows on this machine count towards the last window going; relay windows are excluded.
* [Routing rule order and non-matches](routing-rule-order.md) - First match wins, so rule order matters, and some rules deliberately do not match to let later rules run.
* [A spawn timeout does not mean the spawn failed](spawn-timeout-is-not-failure.md) - The spawn client words timeouts and dropped connections so agents do not start a second helper.
* [Legacy state directory and layout file names](state-dir-renamed-from-perch.md) - Earlier builds used a different directory name and unnormalized layout hashes; the store adopts both on load.
* [Task board corrupt-file and version handling](task-board-file-recovery.md) - A damaged tasks.json is set aside and the board restarts empty, while a newer-format file blocks writes.
* [Unreadable or damaged state files are moved aside, not overwritten](unreadable-state-files-are-set-aside.md) - Layouts, projects, session, groups and prefs that fail to read or parse are renamed with a suffix so a fresh save cannot destroy them.
* [Windows needs a console-linked twin binary](windows-chat-console-twin.md) - An API agent pane runs the chat client, which needs a console program on Windows.
* [The workspace is touched from one goroutine only](workspace-goroutine-only.md) - Every read or write of the workspace must go through Server.do or ask; direct access from a connection goroutine races.
* [Worktree handling for spawned helpers](worktree-spawn-and-cleanup.md) - How spawn reuses, creates, names and rolls back git worktrees, and the checks that avoid data loss.
