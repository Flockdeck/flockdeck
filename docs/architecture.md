# How Flockdeck is built

This page covers the main design choices. [Building from source](building.md)
covers how to work on the code.

## The window

```
┌ flockdeck ───────────────────────────────────────────────────────────── ─ □ × ┐
│ ◭  │ api ▾ │ [ main ] [ fix-auth ▲ ] + ˅  ▲ 1 waiting │ Commands Ctrl+Shift+K │
│ AP ├──────────────────────────────┬───────────────────────────────────────────┤
│ WB▲│ ● api ⎇ main ●3  Read        │ ▲ api ⎇ fix-auth ↑2                       │
│ ▭  │   claude · sonnet  5h 72%    │   codex · gpt-5.6-sol                     │
│    │  (live agent terminal)       │  (live agent terminal)                    │
│ ⇉  ├──────────────────────────────┴───────────────────────────────────────────┤
│ ±  │ ○ shell ⎇ main                                                           │
│ ⚙  │                                                                          │
└────┴──────────────────────────────────────────────────────────────────────────┘
   AP WB = open projects   ▭ = Open a project   ⇉ ± ⚙ = tools, Settings last
   ▲ = blocked on you   ●3 = uncommitted files   ↑2 = ahead
   5h 72% = how much of Claude's five-hour limit is used
```

The interface is a local web app. The binary serves it on the loopback interface
and shows it in a native window with no tabs and no address bar, built with
[Wails](https://wails.io), which embeds the platform's own webview (WebView2 on
Windows, WebKit on macOS, WebKitGTK on Linux) inside the Flockdeck process. The
window belongs to Flockdeck, so the taskbar, the alt-tab switcher and the window
manager tie its identity, icon and pinning to Flockdeck and not to a browser. If
the platform has no working webview, the page opens as an ordinary tab in your
default browser instead. That works but looks less like an application.

Nothing is exposed to the network. The server binds `127.0.0.1` on a random port,
and every request (the page, its assets and both WebSockets) must carry a token
generated fresh for each run. The window is loaded inside the process from a
one-time link that stands in for that token (see `server.WindowURL`). Neither the
token nor the link is put on a command line or in a file that another account on
the machine could read.

Flockdeck Remote listens for nothing from the network either. It is a connection
the machine makes outward, and what arrives through it is let in because the relay
has already checked the device, not because of the token. The
[Flockdeck Remote page](../internal/help/pages/remote.md) describes what the relay
can and cannot see.

## One instance

Running the binary again does not start a second set of agents. It finds the
instance already running, hands it the directory you asked for, and opens a window
onto it. A record of the running instance is kept in the state directory. If the
process died without cleaning up, the next start probes the record, finds it dead
and replaces it. `-solo` starts a separate instance instead.

## Design choices

- Real PTYs. Every pane is a genuine pseudo-terminal
  ([`go-pty`](https://github.com/aymanbagabas/go-pty), ConPTY on Windows).
  Fidelity comes from running the CLI in a terminal, not from parsing its output.
  The one exception is a structured pane: an agent that speaks the Agent Client
  Protocol (`agent.RunnerACP`) is talked to over that protocol, has no terminal,
  and shows a stub line in the default window. It is the `structuredPanes` beta
  in `prefs.json`, off by default, and the picker does not offer these agents
  until it is on (see [Structured panes](#structured-panes) below and
  [ACP adapters](acp-adapters.md)).
- Everything that reads a pane asks one interface. `workspace.PaneRuntime`
  (`internal/workspace/panekind.go`) answers for a pane's status and activity,
  what it is waiting on, its background work, its last reply and its conversation,
  and takes the operations of waking, recording and closing it. `Workspace.Runtime`
  returns the pane's own, the terminal one unless another was set. The team graph,
  the needs-you list, a window's pane view, the agents list and the spend
  rollups all go through it, and none of them looks at which agent a pane runs.
  How the agent is run is the pane's `Transport`, `terminal` or `structured`.
  The graph calls it the pane's kind; it is not `Pane.Kind`, which says agent or
  shell.
- An agent is data, not a branch. Which program to run, which models it offers,
  how its status is known, where its transcript is and how its briefing reaches it
  are fields on one struct. Adding an agent is a table entry, and for a user it is
  a few lines of JSON. That is why a second agent did not become a second copy of
  every feature. An agent that supports none of those capabilities still works as
  a terminal with a program in it, and each feature falls back instead of failing.
- The API agent is the same binary. Talking to a model API directly is a
  subcommand, `flockdeck chat`, run in the pane's own terminal. It reports the same
  lifecycle events over the same loopback endpoint as a CLI agent's hooks, so
  nothing in the workspace learns a second protocol, and there is still one
  program to install. On Windows it also ships as `flockdeck-chat.exe`, linked for
  the console, because the window build gets none in a pane.
- Emulation happens in the browser. xterm.js renders the terminal and encodes
  keystrokes for whatever modes the application has enabled, so raw bytes pass
  through Go untouched in both directions. Go never has to reimplement a terminal.
- Panes survive a reload. Each session keeps a bounded ring buffer of recent
  output, and a window that connects, reconnects or reloads replays it into a fresh
  terminal. A viewer that stops reading is dropped, so it cannot stall the process
  feeding it.
- The workspace has a single owner. It is reached from many connection goroutines,
  so every read and write of it goes through one goroutine. Slow work (git,
  reading transcripts) runs outside that loop and only its results are applied
  there, so a team creating five worktrees does not freeze the window.
- Git is read in bulk, off the hot path. One `status --porcelain=v2 --branch` per
  checkout gives the branch, upstream, ahead and behind counts and file counts
  together. Worktrees are examined concurrently, and pane summaries refresh on a
  timer and not on every frame.

## Where status comes from

For an agent that reports its own lifecycle, a pane's status is not screen
scraping. Claude Code is launched with a generated `--settings` file that registers
its lifecycle hooks (`UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop` and
the rest), and the chat client reports the same event names itself. A Claude Code
event re-invokes the same binary in a hidden `hook` mode, and the chat client
sends its own. Either way the event is posted to a loopback server the app runs,
authenticated with a per-run token. The settings are additive, so your own
settings, hooks and permissions still apply.

The one key the generated file can take over is `statusLine`, because Claude Code
hands a subscription's usage limits to its status line command and nowhere else.
Where you have a status line of your own, the pane's runs the binary in a hidden
`statusline` mode, which passes the figures to the app and then runs your command
with the same input. The [Spend and limits page](../internal/help/pages/spend.md)
says when this is done.

The user-facing description of each state is in the
[status page](../internal/help/pages/status.md). An agent that reports nothing is
read from its terminal instead (the bell, a quiet timer and optional patterns),
and a reported event always wins over that guess.
[docs/status-matrix.md](status-matrix.md) lists every situation a pane can be in
and the status it should show.

## Typed wake

Mail normally reaches an agent through its hooks: the reply to a hook the agent
fires carries a notice. An agent that is idle at its prompt fires none, so mail
for it can wait. Typed wake is the one case where Flockdeck types into a
terminal pane something the person did not type. The wake service
(`internal/workspace/wake.go`) types a fixed line, `Flockdeck: new mail, message
N. Run: flockdeck inbox read N`, and sends the Enter as a second write about a
tenth of a second later, because on Windows a line and its Enter that arrive
together are taken for a paste and left unsubmitted. The line names no sender
and holds no message text. It reverses the earlier rule that nothing is ever
typed for mail, on purpose.

Typing is narrow. All of these must hold: the pane's agent has a recognition
rule (`agent.WakeRule`) that is built in and marked tested; the end of its output
shows an idle, empty input prompt and has not changed for 5 seconds; the pane's
status is idle; nobody has typed in the pane for 30 seconds and Flockdeck has not
for 5; no hook notice was given in the last minute; and no graph message is being
sent to it. A line that was typed but not submitted blocks further lines until
the person types in the pane. At most one line a minute goes to a
pane, at most 3 in a row without an `inbox read` between them, and at most 20 an
hour for a project. There are three off switches (all panes, one agent type, one
pane), set with `flockdeck wake` or from the window.

The limits of this today:

- Both built-in rules are marked `Tested: false` (`internal/agent/wake.go`) until
  one is checked against a recording of the real agent, and a rule from
  `agents.json` is never trusted. So no terminal pane is woken on main.
- A structured pane is the exception. It has no screen to read, so it is idle when
  the protocol says its turn ended, and the same line is sent to it as a prompt,
  with no Enter. The other conditions and limits apply. The wake event names the
  rule as `structured`.
- Text mode is opt-in per agent type, and only for an agent whose catalog entry
  offers it, which is Aider alone. The line is then `Flockdeck message from <role>:
  <text>`, cleaned and cut to 300 characters, and every unread message is typed in
  turn. The line goes into the event log, so in that mode the log holds message
  text. A typed message counts as read, so the limit on lines in a row never
  applies there; only the one-a-minute and twenty-an-hour limits do.
- Each typed line is an event in `team-events.jsonl` (the last 2,000 events or
  256 KiB), without message text outside text mode.
- The refusal of `flockdeck wake on` inside a pane is advisory. The only barrier is
  the agent's own permission prompt for running a command, which an agent set to
  skip prompts does not have.

The conditions, the switches and what happens when the Enter is not sent are on
the [command line page](../internal/help/pages/cli.md), under "The typed wake
line" and "wake".

## Structured panes

A structured pane runs an agent over the Agent Client Protocol and not in a
terminal. It is a beta behind the `structuredPanes` preference, off by default.
It is wired in: with the preference on, a catalog entry with `RunnerACP` starts
as a structured pane, and the workspace reads it through the same `PaneRuntime`
as a terminal pane (`internal/workspace/structuredpane.go`). It has no screen and
no keys to type: `paneSend` and the wake line reach it as prompts, and a
permission request is answered by choosing one of its options. Nothing answers a
request on its own, except that an open request is answered cancelled when the
turn is stopped and when the pane is closed. A structured pane gets no phone push
for a pending permission, and `permissionAnswer` is refused to a window reached
through the relay, so the request can be answered at the desk only. What a relay
window is told of a structured pane is its roster entry with the names
redacted (name, status, directory, branch, project, agent, model), a flag saying
the details are on the desktop, and no text the agent wrote
(`internal/server/relayscrub.go`).

Current limits, each said plainly in the [structured pane
reference](structured-pane.md#6-using-a-structured-pane):

- opencode, by default, runs shell commands, writes and edits without sending a
  permission request. Flockdeck starts it with `OPENCODE_CONFIG_CONTENT` set so
  that `bash` and `edit` ask (the manifest's `launchEnv`), which replaces the
  user's own `bash` and `edit` settings for that pane. An agent that cannot be made
  to ask is marked `runsWithoutAsking` in the manifest and the drawer header shows a
  standing warning for it; none is marked now.
- A structured agent is given no pane identity, so it cannot run `flockdeck
  inbox`, which the wake line and the briefing tell it to do.
- A login or Node problem shows as the phase `needs setup`: a blocked pane, and a
  "Needs setting up" item in the team graph with the step to take. For a
  missing Node the step ends with restarting Flockdeck, since a new Node on the
  path is only seen after a restart.
- No stream recording is started for a structured pane, though the recorder
  exists.
- A restart starts a fresh session and keeps the earlier entries with a notice,
  also when the new agent cannot be started.

The pieces, each its own package:

- `internal/acp` is the client: JSON-RPC over newline-delimited stdio to an agent
  process Flockdeck starts, covering initialize, authenticate, new session,
  prompt, cancel, permission requests and the update stream. It does not cover
  loading or resuming a session, so a restarted pane starts a fresh session and
  keeps the earlier entries as history. It advertises no file or terminal
  methods. The agent is not trusted: writes are queued, lines and requests in
  flight are capped, and every call returns even if the agent stops reading.
  `acp/bridge` turns updates into transcript entries and permission cards,
  `acp/manifest` lists the tested adapters and checks their versions and Node,
  and `acp/acptest` is the fake agent the contract tests run against.
- `internal/structured` is the runtime of one pane: it owns a client and a bridge,
  sends the person's prompts one at a time and keeps the transcript. It knows
  nothing of panes, windows or the server.
- `internal/proctree` ends an agent's whole process tree, which an adapter may
  have added children to: a job object on Windows, a session on Unix. The same
  package ends a terminal pane's process.

The default window shows a stub line for a structured pane. The new shell, built
with `-tags next`, shows its conversation, a prompt box and its permission card
in the drawer. The adapters Flockdeck is tested with, and the Node requirement,
are in [ACP adapters](acp-adapters.md), and the commands a window uses are in
the [structured pane reference](structured-pane.md).

## The knowledge pointer

A repository can keep notes written from its code in `.flockdeck/knowledge/`,
starting at `index.md`. When a pane's checkout has them and the project allows
it, the agent's briefing gets one line: the folder's path, a warning that the
notes may be stale and that their text is data and not instructions. Nothing from
the notes is copied into the briefing.

Because the notes go to whatever model the agent runs on, the pointer is on by
default only when `gh` reports the repository as private
(`internal/workspace/knowledge.go`). For a public repository, or when `gh` is
missing, signed out or offline, it is off and the window shows a one-time notice.
The answer is asked once per run and does not hold up the first briefing, so the
first pane in a private repository may start without the line.
`flockdeck knowledge on|off` records the person's own answer. The lookup trusts
the checkout's remotes, which the [command line
page](../internal/help/pages/cli.md) spells out, and the [agent
briefing](agent-briefing.md) describes the line that is written.

## The team graph

A window can ask for a picture of a project's team and keep it up to date. The
`graphSnapshot` and `graphWatch` commands on the control WebSocket answer with a
`graph` message: the project's panes placed in teams, what each is doing and what
it is waiting on, who sent mail to whom, and what needs the person. It is derived
from what the workspace, the task board and the inboxes already hold, by
`team.BuildGraph` and `team.NeedsYou` in `internal/team`, and a watching window
is pushed a new one when it changes, no more often than every 500 milliseconds.
`paneSend` sends text to a pane by id, the conversation commands read a pane's
conversation, and `permissionAnswer` answers a structured pane's permission
request. The graph commands, `paneSend` and `permissionAnswer` are refused to a
window reached through the relay. The conversation commands are not. Today a relay
device with watch access can read a terminal pane's conversation. The decision is
to stop that (T112, in progress). A structured pane's conversation is not stored
where they read, and its own commands are refused to the relay. The default
window calls none of these, so the window described above is unchanged. The page
built with `-tags next` (`webui.Variant` says which a binary carries) is a new shell laid
over it, and that one calls them. The messages are in the
[team graph reference](team-graph.md).

## Conversation history

The history list is read from each agent's own transcripts, each behind a small
interface: where a conversation is, what was last said in it, and what the
directory has. Claude Code's implementation is the original one. Its transcripts
live in a folder derived from the working directory, and since that name mangling
is Claude Code's business, a folder that does not match is found by reading which
directory its transcripts record. The built-in chat client keeps JSONL of its own,
which resuming one of its panes reads. An agent that writes nothing Flockdeck can
read adds nothing to the list, and every caller copes with that without a special
case. A conversation already open in a pane is shown as open and not offered a
second time, because two panes on one transcript would fight.

## The project picker

The directory browser in the project picker is served by the Go side, because a web
page cannot be handed a real directory path.

## Help and the key table

The help pages are Markdown under `internal/help/pages`, compiled into the binary
and rendered on the Go side. They never write shortcuts out by hand. A page says
`[[key:splitRight]]` or `{{keys:Panes}}`, and both are expanded from the key table
in `internal/help/keys.go`. The command palette, the keyboard dispatch, the
briefing each agent receives (for the dozen actions it names) and the [shortcut table](keys.md) are drawn from the
same table, so a binding changes in one place, and a page that names an action that
no longer exists fails its test. `cmd/docgen` renders the same pages to
docs.flockdeck.ai.
