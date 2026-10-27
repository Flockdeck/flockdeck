# Data types and file formats

* [Agent role](agent-role.md) - A job an agent can be started for: its briefing, worktree policy, default model and who it may spawn.
* [Agent spec](agent-spec.md) - The Spec struct that describes one agent, with its runner, argument template, models, capabilities and environment.
* [agents.json](agents-json.md) - The user's own agents, roles, defaults and routing policy, merged over the built-ins and re-read on every catalog load.
* [remote-artifacts.log](artifacts-audit-log.md) - Append-only JSON-lines audit log of remote artifact connections, refusals and desk changes.
* [Baton store files](baton-store.md) - The batons folder in the state directory and the small bookkeeping files used to age batons out.
* [Baton document](baton.md) - Markdown handoff with front matter and eight fixed sections, written by Render and read by Parse.
* [e2e_key.json](e2e-key-file.md) - This machine's long-term P-256 end-to-end identity used to encrypt terminals and artifacts reached through the relay.
* [Accepted git programs (git-programs.json)](git-programs-file.md) - Record of which git hooks and config-launched programs the user accepted for each repository.
* [Project groups (groups.json)](groups-file.md) - Persisted multi-repo project groups.
* [Helper apps directory layout](helper-install-layout.md) - On-disk layout of installed helpers under the config dir's apps folder.
* [Hook event](hook-event.md) - The Event struct a pane posts to /hook for each agent lifecycle event.
* [Running instance record (instance.json)](instance-file.md) - Record of the running Flockdeck process so later launches attach to it.
* [Jev question and answer types](jev-question.md) - The three question kinds Flockdeck can put to Jev and the answers they produce.
* [keys.json](keys-file.md) - The state-dir file holding API keys the user saved, as a flat agent-id to key map.
* [Saved layout (layout-<hash>.json)](layout-file.md) - Per-project JSON file holding tabs, split tree and pane metadata so a restart reopens the same workspace.
* [Team inboxes (messages.json)](messages-file.md) - Saved inboxes of messages between panes, written whenever an inbox changes.
* [Generated pane settings (<id>.settings.json)](pane-settings-file.md) - Per-pane Claude Code settings file that makes the agent report lifecycle events to Flockdeck.
* [pending.json (staged update)](pending-update-file.md) - Record of an update that was downloaded, verified and unpacked, waiting to be applied.
* [Preferences (prefs.json)](prefs-file.md) - User preferences, including device access roles and remote artifact opt-ins, preserving unknown keys.
* [Recent projects (projects.json)](projects-file.md) - Global list of projects the user has opened, with name, archive flag and manual order.
* [Radar snapshot (gitx.Snap and Scratch)](radar-snapshot.md) - A commit capturing a checkout's committed, uncommitted and untracked work, built in a throwaway object store for conflict detection.
* [Recording transcript line (.jsonl)](recording-line.md) - One JSON object per line, one file per recording session; format version 2.
* [Release manifest and site layout](release-manifest.md) - Signed JSON files and directory layout the download site publishes for each release.
* [remote.json](remote-config-file.md) - This machine's enrolment with a relay: relay address, host id, account id, secret token, name, enabled flag.
* [remote_verified.json](remote-verified-file.md) - Records of paired devices whose end-to-end key fingerprint the person at this machine compared.
* [routing.jsonl](routing-log.md) - Legacy log of model-routing decisions, read back for rule statistics and clearable from Settings.
* [Routing policy](routing-policy.md) - The routing section of agents.json: mode, floor, strategy, crossAgent and ordered rules.
* [Open-projects session (session.json)](session-file.md) - Record of which projects were open when Flockdeck last exited.
* [Task board file](task-board.md) - Per-project tasks.json in the main checkout, written atomically and excluded from git.
* [Task](task.md) - One unit of work on a project's task board, with owner, state, notes and history.
* [Team graph](team-graph.md) - The teams, nodes and waiting-on kinds derived from one project's panes and tasks.
* [Team message and inbox snapshot](team-message.md) - A message in a pane's inbox, the Member roster entry, and the JSON snapshot the inboxes are saved as.
* [Conversation stream entry](transcript-entry.md) - The agent-neutral entry shape that transcript streams produce for a pane's chat view, with its kinds, caps and detail bodies.
* [workspace.Pane](workspace-pane.md) - In-memory pane record, with which fields are persisted and which are runtime only.
* [Worktree process records (worktree-procs.json)](worktree-procs-file.md) - Pane processes running in git checkouts, so a later launch can reap ones it never started.
