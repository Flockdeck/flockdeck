# How Flockdeck is built

This page covers the main design choices. [Building from source](building.md)
covers how to work on the code.

## The window

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
  there, so a fan-out creating five worktrees does not freeze the window.
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
briefing each agent receives and the [shortcut table](keys.md) are drawn from the
same table, so a binding changes in one place, and a page that names an action that
no longer exists fails its test. `cmd/docgen` renders the same pages to
docs.flockdeck.ai.
