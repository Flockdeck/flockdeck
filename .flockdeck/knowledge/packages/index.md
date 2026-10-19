# Packages

* [agent](agent.md) - Catalog of the coding agents, roles and routing policy Flockdeck can start in a pane.
* [appwindow](appwindow.md) - Opens Flockdeck's UI in a native Wails webview window and offers browser and file-manager helpers.
* [artifacts](artifacts.md) - Path-confinement core that turns a file on this machine into something a remote device may be shown read-only.
* [baton pruning](baton-prune.md) - How stored batons and their overflow files are aged out safely, and how damaged records and a wrong clock stop it.
* [baton](baton.md) - Builds, scrubs, stores and frames the handoff document that carries work from one agent to the next.
* [channel](channel.md) - Private per-instance local channel (Unix socket or Windows named pipe) answering identify and window requests.
* [chat model listing and /model](chat-model-listing.md) - How the chat client asks an endpoint which models it has and how /model lists, numbers and matches them.
* [chat rendering and width](chat-rendering.md) - How the chat client wraps a streamed answer to the pane, picks out markdown, counts display width and neutralises control characters.
* [chat streaming parsers](chat-streaming-parsers.md) - The three vendor wires in the chat client, how each reads a streamed answer, and what they share.
* [chat](chat.md) - Flockdeck's own terminal chat client that talks straight to a model API and runs as an agent pane.
* [creds](creds.md) - Resolves the API key an agent needs and keeps it out of logs, JSON and the UI.
* [e2e](e2e.md) - End-to-end encryption of one terminal socket between a paired browser and this desktop.
* [ghcli](ghcli.md) - GitHub access by running the gh command-line tool: PRs, issues, CI checks, repo info, login and install.
* [gitx](gitx.md) - Flockdeck's use of git: status, changes, diffs, commit, push/pull/fetch, worktrees, program scan and conflict-radar snapshots.
* [help](help.md) - Embedded help pages and the single table of UI actions and shortcuts that the palette and help pages render from.
* [helpers](helpers.md) - Downloads, verifies, installs, supervises and opens helper apps (small local programs shipped as signed release archives).
* [hooks](hooks.md) - Loopback HTTP server and clients that carry agent lifecycle events and pane commands back to the running app.
* [idle](idle.md) - Reports how long the machine has had no keyboard or mouse input and whether the screen is locked.
* [jev](jev.md) - Client for TypeSafe's Jev structured-decision API, plus the scrubber for text sent to it.
* [jevgate](jevgate.md) - The single gated path for every question Flockdeck sends to Jev: scrub, bound, cache, rate-limit, pause on failure.
* [keybindings](keybindings.md) - User overrides for Flockdeck's own window shortcuts, kept in keybindings.json.
* [layout](layout.md) - Tree of splits for a tab's panes, with operations and a Compute step that tiles the tree into cell rectangles.
* [legacyenv](legacyenv.md) - Reads pane environment variables under the FLOCKDECK_ name or the old PERCH_ name.
* [pricing](pricing.md) - The single table of per-model token prices, with the day and source each rate was read.
* [radar](radar.md) - Predicts merge conflicts between panes working in one repository by merging their snapshots in memory.
* [record](record.md) - Writes a pane's agent conversation to disk as a redacted, size-capped JSON Lines transcript, and exports stored conversations the same way.
* [recordview](recordview.md) - Reads a recording transcript file and serves bounded, cleaned, re-redacted pages of entries to an untrusted client.
* [remote](remote.md) - Client side of Flockdeck Remote: enrolment, the outbound tunnel to the relay, device roster, end-to-end keys and push sealing.
* [review](review.md) - Auto-review decider that lets read-only Bash tool calls through without a permission prompt.
* [route](route.md) - Pure model-routing engine that picks a model tier for a piece of work from rules in agents.json.
* [secretname](secretname.md) - Decides from a file or folder name alone whether its contents are a secret.
* [selfupdate](selfupdate.md) - Finds, downloads, verifies, stages and applies new Flockdeck releases.
* [server](server.md) - The loopback HTTP and WebSocket server that serves the embedded front end and drives the workspace for every window, local or remote.
* [session hook events and status mapping](session-status-mapping.md) - Which Claude Code hook events a pane subscribes to by version, and how StatusForEvent and ApplyEvent turn events into a pane status.
* [session transcript readers](session-transcript.md) - The transcript subpackage that finds, lists, tails and exports stored agent conversations, with Claude Code and chat readers.
* [session resource usage](session-usage.md) - How a pane's CPU share and memory are measured across its process tree, per platform.
* [session](session.md) - One pane's process on a PTY: launch, output fan-out and replay, status tracking, hook settings, trust inheritance and process-tree shutdown.
* [spend](spend.md) - In-memory book of per-pane token and cost totals and per-account usage windows.
* [store](store.md) - On-disk persistence of layouts, projects, session, preferences, instance record and related state under the per-user config dir.
* [sysproc](sysproc.md) - Starts background child processes without console windows on Windows, and reaps stale processes.
* [tasks](tasks.md) - The per-project task board, its state machine and permission rules, and the notices a change sends.
* [team](team.md) - Roster, inbox messages, mail notices and the derived team graph for the panes of one project.
* [testiso](testiso.md) - TestMain helper that keeps tests off the real config directory, stored keys and network.
* [webui](webui.md) - Embeds the browser front end (HTML, JS, CSS, xterm.js, fonts, icons) into the binary.
* [workspace pane briefing](workspace-briefing.md) - How the workspace builds the text that tells an agent which pane it is in, who else is running and which commands it can use.
* [workspace pane and tab moves](workspace-move.md) - How the workspace rearranges live panes and tabs without stopping any session.
* [workspace task operations](workspace-tasks.md) - How the workspace runs `flockdeck task` operations for a pane or for the person at the window, and which messages a change sends.
* [workspace team panel operations](workspace-team-ui.md) - The workspace methods behind the window's Team panel: roster, board, messages from the person, and closing finished panes.
* [workspace TOML reader](workspace-toml.md) - The small TOML parser the workspace uses to read a Codex config file, and what it accepts and rejects.
* [workspace](workspace.md) - The mutable application state: open projects, tabs, pane trees and live sessions, plus spawning, persistence, messaging, recording and git refresh.
