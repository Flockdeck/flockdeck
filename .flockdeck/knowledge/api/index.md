# HTTP, socket and WebSocket interface

* [GET /assets/*](assets.md) - Serves the embedded front-end files to authorised requests, gzipped when reached through the relay.
* [POST /identify (local channel)](channel-identify.md) - Tells a command-line client which Flockdeck instance owns a socket or pipe.
* [POST /window (local channel)](channel-window.md) - Returns a one-time window link, rate limited to 10 per minute.
* [GET /health](health.md) - Liveness and readiness probe a second launch uses to decide whether a recorded instance is alive.
* [GET /help.json](help-json.md) - Returns the compiled-in help pages as JSON.
* [POST /helpers/start, /helpers/stop, /helpers/open](helpers-endpoints.md) - Lets the CLI start, stop or open a helper app run by this instance.
* [POST /close](hooks-close.md) - A pane asks the application to close another pane, or every finished pane (flockdeck close).
* [POST /hook](hooks-hook.md) - Loopback route where panes report agent lifecycle events; its reply can carry a briefing, a mail notice or an auto-review allow.
* [POST /peer-name](hooks-peer-name.md) - A pane reports the name other Claude sessions use to address it (flockdeck peer-name).
* [POST /spawn](hooks-spawn.md) - A pane asks the application to start another agent pane (flockdeck spawn).
* [POST /task](hooks-task.md) - Task board operations for flockdeck task, selected by an op field.
* [POST /team, /msg, /inbox](hooks-team-messaging.md) - Roster, send-message and read-inbox routes behind flockdeck team, msg and inbox.
* [POST /usage](hooks-usage.md) - A pane reports what its agent has spent in tokens and cost, and subscription windows.
* [HTTP route table](http-routes.md) - Every route registered on the main server mux and which auth each requires.
* [GET /](index-page.md) - Serves the embedded index.html and turns a one-time link or token into a cookie.
* [POST /v1/systemone (TypeSafe, outbound)](jev-systemone.md) - The one outbound call to TypeSafe's Jev API, carrying a scrubbed state and typed questions.
* [POST /open](open.md) - Hands a directory to the running instance so a second launch attaches instead of starting a rival server.
* [POST /quit](quit.md) - Stops the running instance from outside, used by `flockdeck -quit`.
* [POST /remote/artifacts/stop](remote-artifacts-stop.md) - Closes every artifacts socket and reloads preferences after `flockdeck remote artifacts off`.
* [POST /remote/approvals/stop](remote-approvals-stop.md) - Switches phone approval off in memory and closes the approvals sockets after `flockdeck remote approvals off`.
* [POST /remote/reload](remote-reload.md) - Tells a running instance to reread the remote enrolment after `flockdeck remote enable/disable/remove`.
* [POST /window](window.md) - Returns a one-time window link so a second launch can open a window without putting the token on a command line.
* [/ws/artifacts socket](ws-artifacts.md) - End-to-end encrypted, relay-only socket on which a paired device lists and reads this machine's recordings.
* [Control commands: agents, keys, batons, team, recording](ws-commands-agents-team.md) - Commands on /ws/control for agent catalog, API keys, batons, routing, the team graph, recordings and pane info.
* [Control commands: chat, history, attachments](ws-commands-conversation.md) - Commands on /ws/control for the chat view of a pane, session history, resume and image attach.
* [Control commands: git, review, worktrees, GitHub](ws-commands-git.md) - Commands on /ws/control for the review panel, git remotes, worktrees and the gh-based GitHub panel.
* [Control commands: remote access and artifacts](ws-commands-remote.md) - Commands on /ws/control for pairing devices, enabling the relay tunnel, device roles and remote artifacts.
* [Control commands: preferences, helpers, versions](ws-commands-settings.md) - Commands on /ws/control that change saved preferences, manage helper apps and install other versions.
* [Control commands: workspace, tabs, panes, projects](ws-commands-workspace.md) - Commands on /ws/control that change tabs, panes, layout, projects and send prompts.
* [/ws/control socket](ws-control.md) - The window's main WebSocket: JSON commands up, a state snapshot and typed replies down.
* [/ws/pty terminal socket](ws-pty.md) - One WebSocket per pane streaming raw terminal bytes down and keystrokes up, with optional resume, size frames and end-to-end encryption.
* [Control socket server messages](ws-server-messages.md) - Every message type the server sends down /ws/control, with its source and who receives it.
* [state snapshot](ws-state.md) - The whole-workspace snapshot broadcast to every window on /ws/control.
