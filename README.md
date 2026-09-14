# Flockdeck

A desktop application for running several coding agents at once.

Each agent runs in a real pseudo-terminal, so it behaves exactly as it does in
a normal terminal — permission prompts, slash commands, plan mode, colours,
mouse. Around them the app adds what you need to run six at a time: tabs, split
panes, per-agent status, layout persistence, git worktrees and broadcast input.
Each agent is told which pane it is and who else is working, so being one of
several is something it can act on.

The agent and the model are chosen per pane. Claude Code is the default;
beside it Flockdeck runs Codex, Gemini, Aider, opencode or Cursor's agent, and
it talks to a model API directly — its own
chat client in the pane, no wrapper CLI, no node, no Python — including a local
Ollama or any other OpenAI-compatible endpoint.

A CLI agent uses whatever login it already has, so Claude Code panes need no
key and no separate account. Talking to an API directly needs one, and `flockdeck
keys` keeps it.

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

## What it gives you

- **Several agents at once**, each in a real terminal, in tabs and split panes.
- **Any agent, any model, per pane** — a CLI you already have, or a model API
  spoken to directly by the binary itself, picked per pane and remembered per
  project.
- **Rearrange what is already running** — drag a pane to another edge, another
  tab or a tab of its own, or merge two tabs into one, without restarting the
  agent in any of them.
- **Each agent knows where it is** — its own conversation, its own checkout, and,
  for Claude Code and the API agents, a briefing at session start on which pane
  it is and who else is working.
- **One glance tells you who needs you** — per-pane status driven by the agent's
  own lifecycle where it reports one, tab and project markers, and a desktop
  notification when an agent blocks while you are looking elsewhere.
- **What each agent has spent, and how near its limit it is** — an estimate of
  the conversation's cost, or its tokens, in the header of each API agent pane,
  and in a Claude Code pane's once its status line is read (by default, where
  you have one of your own), with a subscription's five-hour and weekly
  windows. Worked out on this machine, and sent no further than your own
  paired devices.
- **Multiple projects open together**, switched without stopping anything.
- **Worktrees as a first-class thing**: create, inspect, occupy and remove them
  without leaving the app.
- **Everything comes back**: layouts, the set of projects you had open, and
  each pane's conversation.
- **Agents can outlive the window** — detach, close it, reattach later.
- **Reach them from another device** — pair a laptop, tablet or phone through
  a relay, with no port opened on this machine.
- **One agent's plan becomes several agents doing the work**, each in its own
  git worktree.
- **The right model for each task** — routing rules, off until you turn them
  on, pre-set a fan-out's rows to a smaller model for mechanical work and a
  stronger one for hard work, shown for you to change before anything starts.
- **Review, commit and push** what an agent did without leaving the app.

## Why

Running one agent is easy. Running several is not: they finish at different
times, they block on permission prompts, and you lose track of which one is
waiting on you. Flockdeck answers one question at a glance — **which agent
needs me right now** — and gives each agent its own branch to work on.

## Install

Flockdeck itself needs nothing but the binary. What a pane runs is another matter:
a CLI agent has to be on your `PATH` — [Claude Code](https://claude.com/claude-code)
for the default — and an API agent needs a key. The picker shows every agent it
knows about either way, greying out the ones this machine has not got and
saying where to get them.

```sh
curl -fsSL https://flockdeck.ai/install.sh | sh   # macOS and Linux
irm https://flockdeck.ai/install.ps1 | iex        # Windows, in PowerShell
```

Either one installs the release it was published with. The site is
regenerated for every release, and that writes the release's version and the
SHA-256 of each of its archives into both scripts. The script downloads the
archive for the machine from `dl.flockdeck.ai`, or from GitHub when that
cannot be reached, and checks it against the SHA-256 it carries itself, not
against a `checksums.txt` fetched from where the archive came from. Then it
puts it in a directory you own — `~/.local/bin`, or
`%LOCALAPPDATA%\Programs\flockdeck` with a Start menu shortcut on Windows — so
neither installing nor updating ever asks for admin rights. The scripts are in
`cmd/sitegen/assets`, beside the page that serves them.

With that pinned release installed, if the site has moved on since — the site
is only regenerated on a release, but the script you fetched today can be
older than that — it asks the binary it just installed to update itself with
`flockdeck update`, so you land on the latest release even from a page
sitting behind a CDN's cache. That update is checked by the release
signature, the same way every update after it is; the script itself checks
nothing but the pinned archive above.

Both read a few settings from the environment:

- `FLOCKDECK_VERSION` — another release, such as `v0.2.8`, which is left
  exactly as installed rather than moved to the latest. The script carries
  checksums for its own release only, so any other is checked against the
  `checksums.txt` downloaded beside it. To also keep Flockdeck itself from
  updating once it runs, set `FLOCKDECK_UPDATE=off` where it runs.
- `FLOCKDECK_INSTALL_DIR` — another directory to install into.
- `FLOCKDECK_DOWNLOAD` — a mirror to fetch the release files from instead,
  laid out as `<mirror>/<version>/<file>`. It is the only place asked, with no
  falling back to GitHub, and it also skips moving to the latest, which a
  mirror may not carry. It is asked for the script's own release unless
  `FLOCKDECK_VERSION` names another, and for that release's `checksums.txt`
  too when it does.
- `FLOCKDECK_NO_MODIFY_PATH=1` — Windows only: leave `PATH` and the Start menu
  alone. The script then says how to start Flockdeck by its path.

```sh
curl -fsSL https://flockdeck.ai/install.sh | FLOCKDECK_INSTALL_DIR=~/bin sh
```

```powershell
$env:FLOCKDECK_VERSION = 'v0.2.8'; irm https://flockdeck.ai/install.ps1 | iex
```

With Go:

```sh
go install github.com/jmwri/flockdeck@latest
```

Or from a clone:

```sh
make build      # a binary for this machine
make dist       # binaries for all six supported platforms
```

Where there is no `make`, as on many Windows machines, `go build .` builds the
same program; on Windows add `-ldflags -H=windowsgui`, as the Makefile does,
so that it opens without a console window behind it.

The whole program builds with `CGO_ENABLED=0`, including the PTY layer and the
front end, so `windows`, `linux` and `darwin` on both `amd64` and `arm64` all
cross-compile from any one machine with nothing but the Go toolchain. There is
one artifact: a single binary with no assets to install beside it. The Windows
release also carries `flockdeck-chat.exe`, the same program linked for the
console, which is what an API agent's pane runs there.

### Staying up to date

Every release is published at `https://dl.flockdeck.ai` under its version, as
one archive per platform — a `.zip` for Windows, a `.tar.gz` elsewhere — with
a `checksums.txt` and a `manifest.json` describing them. Both are signed with
the release's Ed25519 key, whose public half is built into Flockdeck, and
nothing under a version changes once it is published. A second, standby key
has been trusted alongside it since v0.3.5: its private half is held offline,
by hand, and never touches a build or a workflow, so it can sign one release
if the first key is ever lost or compromised. `latest.json` names
the latest release, `{"version":"v1.2.3"}`, and nothing more. It is not
signed, and is not purged from the CDN when it moves, because it needs
neither: all it can do is point at a release whose own manifest is signed. An
old or forged one can hold an update back for as long as it is cached, and
can never have anything unsigned or older installed. GitHub carries every
release as well, as a mirror. Tagging a commit `v1.2.3` is the whole of cutting one: the
workflow vets, tests, cross-builds all six, publishes them on GitHub, signs
them and uploads them to `dl.flockdeck.ai` (`scripts/publish-downloads.sh`). A
tag with a suffix, `v1.2.3-rc.1`, is a pre-release: it is published under its
version and never becomes the latest.

The install scripts above use the same `latest.json` to decide, once their
own pinned release is in and checked, whether to run `flockdeck update`
right away and land you on the latest release instead — unless
`FLOCKDECK_VERSION` or `FLOCKDECK_DOWNLOAD` said to stay put.

A running Flockdeck watches for releases and downloads anything newer in the
background. It asks `dl.flockdeck.ai` first, and trusts only what carries the
release key's signature; when the site cannot be reached, answers with
something else, or fails a signature, it goes to GitHub instead and logs why.
Either way the download is checked against its published SHA-256, and the
request carries nothing that tells one installation from another. Nothing is replaced
while you are working. When a release is ready an **Update** button, naming the
version, appears at the right of the top bar, and installing it now is a
restart you ask for: the layout is saved and
reopened, though the agents running in panes are stopped, which is why it is
never done for you. Otherwise it goes in as Flockdeck next quits, unless
`FLOCKDECK_UPDATE=off` or **Check for updates** is off in Settings → General,
so the start after that is the new version.

From a terminal:

```sh
flockdeck update          # fetch the latest release and put it in place
flockdeck update -check   # say whether there is one, and stop
```

Replacing the binary leaves a running instance alone — it is already loaded —
so the new version is what starts next time. A build you made
yourself — stamped `dev` by `go build`, or by `git describe` when built with
make — is never replaced by a release. `FLOCKDECK_UPDATE=off`, or turning
off **Check for updates** in Settings → General, turns the background check
off, and stops an update already downloaded being put in place when Flockdeck
exits; the subcommand still works.

### Uninstalling

Turn remote access off first if it is on (`flockdeck remote disable`), so the
relay forgets this machine. Then quit Flockdeck and delete it:
`~/.local/bin/flockdeck`, or on Windows the `%LOCALAPPDATA%\Programs\flockdeck`
folder, its Start menu shortcut and its entry in your PATH. Settings, layouts
and keys are kept apart, in `%AppData%\flockdeck`,
`~/Library/Application Support/flockdeck` or `~/.config/flockdeck`; delete that
too, and on Windows `%LOCALAPPDATA%\flockdeck`, which holds the window's
browser profile. Worktrees Flockdeck made are ordinary git worktrees beside your
repositories, and stay until you remove them.

## Running it

Double-click the binary, launch it from a shortcut, or run it from a terminal —
all three work. It opens its own window. On Windows there is no terminal to keep
around. On macOS and Linux, a Flockdeck started from a terminal runs in it, and
so does a binary double-clicked on macOS, which opens one. Closing that terminal
quits an attached Flockdeck the orderly way, saving the layout and stopping the
agents. A detached one carries on, whether it was started with
`flockdeck -detach` (which gives the terminal back) or detached from the
command palette.

```sh
flockdeck                 # open the current directory, or the projects open last time
flockdeck -C ~/code/api   # …or attach to a running instance and open it there
flockdeck -new            # start without the saved layout, and replace it as it runs
flockdeck -shell          # first pane is a shell, not an agent
flockdeck -agent codex    # every new pane this run is that agent
flockdeck -detach         # run with no window; attach to it later
flockdeck -quit           # stop a running instance and its agents
flockdeck -no-window      # just serve; print the URL and open it yourself
flockdeck -solo           # start a separate instance instead of attaching
flockdeck -version        # print the version

flockdeck agents          # the agents it can run, and which are installed here
flockdeck keys set openai # give an API agent a key, read from stdin
flockdeck keys list       # which agents have one, not what it is
flockdeck keys clear openai # forget the key Flockdeck stored
flockdeck keys check openai # ask the API whether it takes the key
flockdeck keys endpoint openai-compatible <url>  # point an API agent at another address
flockdeck keys endpoint openai default  # back to the vendor's own address

flockdeck remote enable   # reach this machine from another device, via a relay
flockdeck remote pair     # a one-time link and QR code that pairs a device
```

Once it is running you rarely need the command line again: projects are opened
and switched from inside the window, and so is the agent each pane runs.

### One instance, attached and detached

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it — so `flockdeck -C ~/code/api` from anywhere adds that project to
the session you already have. A record of the running instance is kept in the
state directory; if the process died without cleaning up, the record is probed,
found dead and replaced.

`Detach` (in the command palette) closes the window and leaves every agent
running. Start that way with `-detach`, come back with `flockdeck`, and stop
everything with `flockdeck -quit`. Closing the window normally still quits, so
nothing is left running by accident.

### How the window works

The interface is a local web app that the binary serves on the loopback
interface and displays in a **chromeless application window** — no tabs, no
address bar. It looks and behaves like a native window while keeping the
program a single dependency-free binary.

That window is provided by a Chromium-based browser in app mode: Chrome, Edge,
Brave, Chromium or Vivaldi, whichever is found first. On Windows this is always
satisfied because Edge ships with the OS. If none is installed the page opens
as an ordinary tab in your default browser instead, which works but looks less
like an application. `FLOCKDECK_BROWSER` forces a specific one.

Nothing is exposed to the network: the server binds to `127.0.0.1` on a random
port and every request — page, assets and both WebSockets — must carry a token
generated fresh for each run. The browser is never started with that token, or
the one-time link that stands in for it, on its command line — another
account on the same machine can often read one process's command line from
another's — so the link is written to a file only your account can read
instead, and the browser is pointed at that file. [Remote access](#remote-access), below, opens no port either: it
is a connection this machine makes outward, not one it accepts, and what
arrives through it is let in because the relay has already checked the device,
not by the token.

## Keyboard shortcuts

Everything not listed here goes to the focused agent, which needs the rest of
the keyboard for itself. Press `F1` in the window for the same list, alongside
the rest of the help.

This table is generated from the one key table in `internal/help/keys.go`,
which the command palette and the in-app help are also drawn from; run
`go test ./internal/help -run TestREADMEShortcuts -update` after changing it.

<!-- shortcuts:start -->

### Panes

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+D` | Split right (agent) |
| `Ctrl+Shift+E` | Split down (agent) |
| Command palette | Split right (choose agent)… |
| Command palette | Split right (shell) |
| `Ctrl+Shift+←` | Move pane left |
| `Ctrl+Shift+→` | Move pane right |
| `Ctrl+Shift+↑` | Move pane up |
| `Ctrl+Shift+↓` | Move pane down |
| Command palette | Move pane to a tab of its own |
| Command palette | Tile these panes evenly |
| `Ctrl+Shift+Z` | Zoom pane |
| Command palette | Restart pane |
| `Ctrl+Shift+W` | Close pane |

### Tabs

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+T` | New agent tab |
| Command palette | New agent tab (choose agent)… |
| `Ctrl+Shift+N` | New shell tab |
| `Ctrl+Tab` | Next tab |
| `Ctrl+Shift+Tab` | Previous tab |
| `Alt+1 … Alt+9` | Select tab by number |
| Command palette | Merge every tab into this one |
| Command palette | Move tab left |
| Command palette | Move tab right |

### Agents

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+B` | Toggle broadcast |
| Command palette | Add this pane to broadcast, or take it out |
| `Ctrl+Shift+P` | Prompt all panes |
| `Ctrl+Shift+X` | Fan out — turn this pane's plan into agents |
| `Ctrl+Shift+A` | All agents across projects |
| Command palette | API keys… |

### Git

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+G` | Worktrees |
| `Ctrl+Shift+S` | Review changes, commit and push |

### Finding your way

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+K` | Command palette |
| `F6` | Move to the next part of the window |
| `Shift+F6` | Move to the previous part of the window |
| `Ctrl+Shift+F` | Find in terminal |
| `Ctrl+Shift+R` | Resume a past conversation |
| `Ctrl+Shift+O` | Projects |
| `F1` | Help |

### The window

| Keys | Action |
| --- | --- |
| `Ctrl+,` | Settings |
| `Ctrl+=` | Increase font size |
| `Ctrl+-` | Decrease font size |
| `Ctrl+0` | Reset font size |
| Command palette | Remote access… |
| Command palette | Detach — close the window, leave agents running |
| Command palette | Quit — stop every agent in every project |

<!-- shortcuts:end -->

Panes are focused by clicking, resized by dragging the divider between them
(or from the keyboard: `Tab` to it, then the arrow keys, and `Home` to share
the room equally), moved by dragging their header, and closed, restarted or
zoomed from the buttons in their header. In any dialog `Esc` closes it and the
arrow keys and `Enter` work through its list.

## How each feature works

### Agents and models

A pane is a shell or an agent, and an agent is two choices: which agent, and
which model. Both are made per pane, defaulted per project, and remembered with
the layout.

The plain split and new-tab keystrokes take the project's default and stay one
keystroke, because that is what you want almost every time. The picker — the
caret beside the `+` in the tab bar, or the command palette — is for choosing
deliberately. It lists agents as **installed** and **not installed**,
each expanding to its models with the default marked, and offers *set as
default for this project* at the foot. An agent you have not got is greyed with
where to get it rather than hidden: somebody who has never installed Codex
should still learn that Flockdeck would run it. An OpenAI-compatible endpoint
is given its address there too: pick it, type where the model server answers,
and one on this machine, which needs no key, is offered at once. `/v1` is
added only to a bare address such as `http://127.0.0.1:11434`; one with a path
of its own, such as a gateway's `https://gateway.example/openai`, is used as it
stands. Each model
shows its tier — *small*, *mid* or *top*, how capable and so how costly it is
among that agent's own — and an API agent's models their published price per
million tokens with the day it was read; a CLI agent's show none, since it may
be on a subscription. The pane header then names what it
got beside the branch, in the same dim weight — `claude · sonnet`,
`codex · gpt-5.6-sol`.

There are two ways an agent gets run:

- **A CLI**, started in the pane's pseudo-terminal exactly as you would start
  it yourself, using whatever login it already has. Claude Code, Codex, Gemini,
  Aider, opencode and Cursor's agent are built in.
- **An API, spoken to directly.** `flockdeck chat` is Flockdeck's own terminal chat
  client, run in the pane, talking straight to a model API: Anthropic, OpenAI,
  Google, and any OpenAI-compatible endpoint, which is how a local Ollama, LM
  Studio or vLLM — or a gateway — becomes an agent. No wrapper CLI, no node, no
  Python; one binary. It streams, it renders code and tool calls, it carries the
  model and its token counts on a status line (and the running cost, for the
  Anthropic, OpenAI and Google models in its dated price table), and it has the file and command
  tools an agent needs: the file tools confined to the pane's working
  directory, commands started in it, and each asking before it writes or runs
  anything, unless you have answered *always* to a command like it this
  session. That ask is reported as a
  lifecycle event, so the pane turns amber and the user is told which pane
  wants them — which is the whole point of this application.

An agent is a table entry rather than a branch in the code. Each one says what
it runs, what models it offers, and which of Flockdeck's facilities it can support:
whether it reports its own lifecycle (so status is a fact rather than a guess),
whether it can reattach a conversation by id (so restoring a layout is more
than cosmetic), whether it writes a transcript somewhere readable (so fan out
reads its plan and the history overlay lists it), whether it has a trust
question worth answering ahead of a fan-out, and how its briefing reaches it.
An agent that supports none of it still works: it is a terminal with a program
in it, and every one of these features falls back rather than failing.

Your own agents go in `agents.json` in the state directory, which is overlaid
on the built-ins by `id` — field by field, so `{"id": "claude", "defaultModel":
"sonnet"}` changes the default model and nothing else. An unknown id is a new
agent; `"hidden": true` takes one out of the picker. The file is re-read every
time the picker opens, so editing it by hand needs no restart, and a file that
does not parse is a notice in the interface rather than a failure to start.

Keys for the API agents are resolved from that agent's own environment
variables first, then from `keys.json` in the state directory, written by
`flockdeck keys set <agent>` reading stdin, and last from `FLOCKDECK_API_KEY`,
which every API agent reads. A vendor's own variable, such as
`OPENAI_API_KEY`, is read only by an agent talking to that vendor's own
address: a built-in pointed at a gateway or a proxy is never sent the key you
exported for the vendor, and uses the one stored for it. A key reaches exactly one place — the
environment of the chat process for the pane that needs it — and is never
logged, never in a snapshot, never in an error message. The interface shows
*set* or *not set*, offers *set…* and *clear*, and never reads one back.

### Attention indicators

Each pane header shows a status dot, any tab containing an agent that is
blocked on you is marked `▲`, and the window title reports the count so a
waiting agent is visible in the taskbar even when the window is not focused.

The application icon carries the same state. The window is a Chromium app-mode
window, so the icon in the taskbar is the page's own, and it changes with the
agents: grey when nothing is running, accent cyan while an agent works, and
amber the moment one blocks on you. Colour is spent on it for the same reason
it is spent anywhere else here — it is reporting a state the app actually
knows, not decorating the window.

| Dot | Meaning |
| --- | --- |
| green, pulsing | Working — producing output or running a tool |
| amber | **Waiting on you** — a permission prompt or a question |
| grey | Idle — finished its turn, ready for a new prompt |
| faint grey | Starting — launched, and not heard from yet |
| red | The process exited |

A helper another agent started for itself (see [An agent starting its own
helpers](#an-agent-starting-its-own-helpers)) raises none of this while that
agent's own pane stays open — its idle nudge is the agent's to notice, not
yours, though a permission prompt or a real question still reaches you as
usual, and once that pane is closed the helper's idle nudges do too.

For an agent that reports its own lifecycle, this is not screen scraping. A
Claude Code pane is launched with a generated `--settings` file registering its
lifecycle hooks (`UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop`, …),
and an API pane's chat client reports the same event names itself, so nothing
in the workspace has to learn a second protocol. A Claude Code event re-invokes
this same binary in a hidden `hook` mode, and the chat client sends its own;
either way it is posted to a loopback server the app runs, authenticated with a
per-run token. Status therefore reflects
what the agent is actually doing rather than what its output happens to look
like, and `PreToolUse` even surfaces the running tool's name in the pane header.

The settings are additive — your own settings, hooks and permissions still
apply.

The one key the file can take over is `statusLine`, because Claude Code hands a
subscription's usage limits to its status line command and nowhere else. Where
you have a status line of your own, the pane's runs this binary in a hidden
`statusline` mode, which passes the figures to the app and then runs your
command with the same input, so the line under the prompt is the one you had.
Where you have none it is left alone unless Settings says otherwise, since any
status line hides the keyboard hints in Claude Code's footer. The header then
shows the five-hour and weekly windows beside what the conversation has cost;
the help's *Spend and limits* page has the rest.

Not every coding agent has a lifecycle to register, and Flockdeck runs those too.
For them the status is read from the terminal instead: the bell, a quiet timer,
and — for an agent whose `agents.json` entry gives them — patterns for the two
lines that matter, the shape of a permission question and the shape of a
prompt waiting to be typed at. They are matched against the last few hundred
bytes with the escape sequences stripped, never against the whole scrollback,
and a reported event always beats a pattern. None of the built-in agents has
patterns yet. It is a guess where the other is a fact, and the help says which
agents are which so you know which you are looking at.

### Spend and limits

Each pane header says what its agent has spent in the conversation and the
tightest usage limit it runs under, so the agent that is burning money, or
about to stop, can be picked out of a tab of six.

- **An API agent** reports every call's tokens, counted exactly, and Flockdeck
  prices them from its dated price table: `~$1.24`, or `84k tok` for a model
  with no price, or `~$0.04+` when some of the tokens had none, which makes
  the figure a floor.
- **Claude Code** hands its figures to its status line, above, so they are read
  by default only where you have a status line of your own; Settings › Agents
  › Claude Code's usage limits › *Always* reads them in every Claude pane, at
  the cost of Claude's footer hints where you have none. On a Pro or Max
  plan the header leads with the tightest window, `5h 72%` over a small meter,
  amber at 80% and red at 95%, and shows tokens rather than dollars; the
  tooltip lists every window and when it resets, and what the tokens would
  cost on the API. On an API key it shows Claude Code's own estimate of the
  session, `~$0.50`.

Every money figure is an estimate, written with `~`, and its tooltip ends *An
estimate at published prices, not your bill.* A limit belongs to the login,
so every Claude pane on it shows the same windows. Nothing is fetched to work
any of this out, and the figures go no further than the pane state your own
paired devices receive when remote access is on. They are kept in memory, and
start again with a new conversation or a new run of Flockdeck.
Codex, Gemini CLI, Aider, opencode and Cursor Agent report nothing yet, so
their panes show neither.

### Model routing

Every built-in model has a tier — `small`, `mid` or `top` — and one table in
`internal/pricing` holds every price the app states, each rate dated with the
day it was read from the provider's own pricing page. A release build's tests
fail when any rate is more than 120 days old, so the table is read again for
each release; it is never fetched at run time.

On those tiers, rules in `agents.json` pre-set a fan-out's rows to a model
suited to the work: the built-in ones move running the tests, a short rename
or move, and changelog or README wording to the smallest model, and a
refactor, a race, a migration or a security fix to the strongest. Routing is
**off** until it is turned on in Settings › Agents › Routing, for every project
or for one, and a project's own policy replaces the other whole.

What it chooses is shown in the dialog before anything starts: a routed row's
select is pre-set, tagged `↘ routed` or `↗ routed` with the rule in its
tooltip, and one button puts every row back on the run's model. The server
never routes a fan-out itself — the dialog sends the rows as though they had
been chosen by hand — so what was shown is what runs. A pane started on a
routed model says so in its header, `claude · haiku ↘`.

Routing only ever changes the model, never the agent, and only between models
whose tier it knows; Claude Code's *Default*, which is whatever the CLI is set
to, is left alone. It makes no request of any kind. What it chose, and whether
the choice was kept, goes in `routing.jsonl` in the state directory: rule
names and model ids, never the task.

### Rearranging what is already running

A layout is rarely right first time: the agent you thought was a side errand
turns out to be the one you are watching, and it is in the wrong corner. Panes
are therefore movable, not just creatable.

**Drag a pane by its header.** The pane under the pointer shows where the
dragged one would land if you let go there:

- onto the **left, right, top or bottom** of another pane — it goes there,
  splitting that pane's space, joining an existing row or column rather than
  nesting a new one inside it;
- onto the **middle** of another pane — the two exchange places, and the
  layout keeps its shape and proportions exactly as they were;
- onto **another tab** in the tab bar — it moves into that tab;
- onto the **`+` button** — it gets a tab of its own.

**Drag a tab by itself**, and where along another tab it lands decides what
happens:

- onto the **left or right end** of another tab — it is reordered to there;
- onto the **middle** of another tab — the two tabs are **merged**, and one tab
  is left holding every pane of both. Each tab keeps the arrangement it had, and
  the room is shared out a column at a time rather than half to each tab, so
  five tabs folded in one after another are five even columns rather than a half,
  a quarter, an eighth and two slivers;
- onto the **`+` button** — it goes to the end of the bar.

A tab emptied by dragging its last pane away closes itself, and **the pane is
not closed with it** — that is the difference between moving a pane out and
closing it. Merging is the reverse of dropping a pane on `+`: what one splits
apart the other gathers back up.

From the keyboard, `Ctrl+Shift+←`/`→`/`↑`/`↓` moves the focused pane past its
neighbour in that direction; the neighbour is chosen by what is on screen
rather than by tree order, so the opposite arrow always puts it back. The
command palette carries the same moves, plus "Move this pane to tab: …" and
"Merge tab into this one: …" for every open tab, and "Merge every tab into this
one" for when the agents you want to watch together are scattered across all of
them.

Every one of those moves is relative — beside this pane, past that one — and
enough of them leaves a tab with panes too narrow to grab a divider in. "Tile
these panes evenly" is the way back: rows of even columns, in the order the
panes are already in.

Nothing is started or stopped by any of it. A move relocates the pane's leaf in
the layout tree, so the process, its conversation, its working directory and its
scrollback all come with it. The rearranged layout is saved like any other, so
it comes back on the next run.

### Each agent knows where it is running

An agent started in a pane would otherwise have no idea it is one of six. It
does not know that its neighbour is editing the same repository on another
branch, that the directory it was dropped into is a worktree rather than the
project, or that the thing it was asked to do came from another agent's plan.

So Claude Code and the API agents are told, in their own words, when they
start:

- which pane it is, in which tab and which project;
- the directory and branch it has, and whether that is a worktree of its own
  rather than the project root;
- what it was spawned to do, when it was started by a fan-out or by another
  agent rather than by hand;
- which other agents are running beside it, where each of them is working,
  which agent and model each of them is — `codex · gpt-5.6-sol`, because what is in
  the next pane changes what is worth asking of it — and what each was asked
  for, and that their conversations are separate, so nothing passes between
  panes except through the user or a commit;
- that it can start agents of its own with `flockdeck spawn`;
- what the application around it can do — the status the user is watching, the
  fan-out that reads its own output, broadcast, the diff and the worktree
  panel, what a restart keeps — with the keys for each, taken from the same
  table the command palette and the help pages are drawn from, so a user who
  asks how to do something is answered by the agent in front of them;
- what its pane carries in its environment, and what the rest of the command
  line does — including that `-quit` stops every agent in every project rather
  than only this pane.

Where the agent has a session-start hook — Claude Code does, and so does the
built-in chat client — it is one more lifecycle event, `SessionStart`, answered
by the same loopback server that receives the status events. The reply is
returned as `additionalContext`, which is the supported way to add to a session,
so nothing is typed into the terminal and no settings of the user's are
overwritten. `SessionStart` fires again after a compaction and on resume, so a
pane that has been running all day is still oriented after its context has been
summarised away.

An agent with no hook to answer can have the same briefing put in front of its
opening prompt instead, when its entry in `agents.json` sets
`"caps": {"context": "prompt"}`. It is fenced in a `<flockdeck-context>` block
so the agent can tell the two apart, and sent alone if there is no opening
task. That is once per launch rather than once per compaction, which is honest
as long as the briefing says when it was taken, and it does. None of the
built-in CLI agents asks for it, so Codex, Gemini CLI, Aider, opencode and
Cursor Agent start unbriefed.

Panes also carry `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME`, `FLOCKDECK_PROJECT`,
`FLOCKDECK_AGENT` and `FLOCKDECK_MODEL` in their environment. The first three are what
a shell pane — with no lifecycle hooks of its own — has to go on; the last two
are how a script or a prompt can say what it is sitting in. They also carry
`FLOCKDECK_LAUNCH`, new each time the pane starts, which its hooks send back so
that a hook still running from before a restart cannot change the new process's
status. Every `FLOCKDECK_*` pane variable except the agent, model and launch,
`FLOCKDECK_API` and
`FLOCKDECK_TOKEN` included, is also set under its old `PERCH_*` name, so a
shell prompt written against the old names keeps working until a later release
drops them.

The isolation this describes is real rather than advisory. Each pane is a
separate top-level session with its own session id, its own generated settings
file where the agent takes hooks, and an environment scrubbed of the markers a
parent agent session would otherwise pass down. Those markers are stripped for every agent in the catalog
rather than only the one in this pane, because Flockdeck may have been launched
from inside any of them; nothing is shared between two panes.

### Session persistence

Layouts are saved per project. On the next run the tabs, splits, proportions
and working directories come back, **every project you had open is reopened**,
each pane starts the agent and model it had, and each one resumes the
conversation it had before rather than starting an empty one. The project you
name on the command line is the one you land in; the rest are restored around
it. A project whose folder is missing at start — a USB stick, a network drive
not yet connected — is not opened, but stays in the list for the next ten
starts, so it comes back with its folder.

That works because panes are identified by a UUID handed to the agent as its
session id when the pane is created, and handed back to reattach when it is
restored. Pane identity and conversation identity are the same thing, which is
what makes restore meaningful rather than cosmetic.

Resuming is attempted only where the agent can reattach by id **and** has
actually written a transcript. `claude --resume` exits immediately if there is
nothing to resume, so a pane that was opened but never prompted starts a fresh
conversation instead of dying on restore or restart; and a pane running an
agent that cannot resume comes back in the right tab, in the right directory,
with an empty conversation rather than an error.

A layout written by a build that only knew about Claude is read without a
murmur: its `claude` panes become agent panes running `claude` with no model
pinned, migrated in memory and written back in the current format. Nothing is
lost and nobody is asked anything.

### Fan out: one agent's plan, several agents doing it

Ask an agent to plan something and it answers with a list. `Ctrl+Shift+X` reads
what that agent last said, pulls the list items out of it, and offers them —
editable, one per line — as a set of agents to start. Nothing runs until you say
so; the extracted list is a suggestion, not a decision.

For a Claude Code pane, what it reads is the agent's own transcript, not the
pane's screen. The screen is a redrawn interface: bullets are wrapped to the
pane's width and so cut mid-sentence, the status line begins with a glyph
indistinguishable from a bullet, and the agent's thinking sits in the same
column as its answer — all of which arrives looking like a plan. The
transcript is the markdown the agent actually wrote. Every other pane — a
shell, or any other agent, the built-in chat client included — still falls
back to the screen.

The list is narrowed to what reads as work. Nested bullets are detail about a
job rather than jobs of their own; entries under a line that announces a plan
win over the findings above it; a question is something to answer rather than
something to do. It is a heuristic over prose, so it will still be wrong
sometimes — which is why the list arrives in a text box.

Each child can take **its own git worktree**, on a branch named after its task,
so several agents work in parallel without touching each other's files. Each is
a normal pane: watch it, type into it, review and commit its work from the
Changes panel.

The children share **one tab**, laid out in rows of even columns — three panes
are a row of three, twelve are three rows of four. A tick box puts them in the
tab the plan came from, beside the agent that wrote it, instead of a new one
called **Fan out**. A tab each was the old behaviour and it was the wrong one:
a dozen agents made a dozen tabs nobody could read, and a fan-out is exactly
when you want to see them all at once.

The fan-out then shows you what it started: its tab is selected and the first
agent has the focus, or, in the tab the plan came from, the focus moves to the
first new pane. If you have gone to another tab while the worktrees were being
made, the window is left where you are.

The dialog carries one agent-and-model control for the whole run and an
override on each row, so twelve tasks can be split between two agents
deliberately — the capable one for the refactor, the cheap one for the six
renames. The run starts on the project's default agent and model, and with
[routing](#model-routing) on for the project, rows its rules match come with a
model already chosen.

The task is handed to the agent as its opening argument rather than typed into
the terminal, so it is submitted the moment the agent starts rather than
depending on guessing when the interface is ready.

There is one wrinkle worth knowing about, and the dialog handles it: a fresh
worktree is a directory the agent has never seen, and an agent with a trust
question of its own — Claude Code has one — would stop and ask whether the
folder is trusted before doing any work, once per child. If the project you are
fanning out from is already trusted, the dialog offers to carry that same
answer over to the worktrees it creates. It is a checkbox, it says what it
does, and it will not invent trust: inheriting is refused unless the source
directory is genuinely trusted already. The project's answer to Claude Code's
second question, "Allow external CLAUDE.md file imports?", comes across the
same way: a yes stays a yes, a no stays a no, and nothing is written if the
project was never asked. The questions are Claude Code's, so it does nothing
for another agent.

#### An agent starting its own helpers

Every pane is given an address and a token in its environment, so an agent can
hand work to helpers itself:

```sh
flockdeck spawn "add tests for the parser"
flockdeck spawn --worktree fix-auth "repair the token refresh"
flockdeck spawn --split "watch the build"
```

Ask a lead agent to plan and then run one of these per task, and it fans itself
out. Only processes running inside a pane can do this: the token never leaves
the environment the pane was started with.

A helper started this way is its parent's responsibility, not yours: its
finishing and going quiet does not raise a phone push, a desktop notification
or count toward the waiting badges, for as long as its parent's pane stays
open to notice instead. A helper asking permission or a real question still
turns amber and reaches you as usual, since only you can answer those. Once
the parent's pane is closed, its helpers are yours again, from their next idle
moment on.

### Review, commit and push

`Ctrl+Shift+S` shows what changed in the working tree an agent has been using:
each file with what happened to it and how many lines moved, a coloured diff of
whichever file you select, and the branch's position against its upstream.
From there you can commit, commit and push, pull or fetch. The first push sets
the upstream, so a branch a fan-out invented does not need a hand-typed command
to leave the machine. A commit takes the files the list showed you; one that
arrives or is written to again after you looked is marked, and a commit made
before you have seen it is refused.

The worktree panel has a **Review** action per checkout, which is usually how
you get here: see which agent produced something, then look at what it did.

### Conversation history

`Ctrl+Shift+R` lists the project's stored conversations — the opening prompt of
each, how long ago it was touched, how many entries it holds, and its session
id — and resumes any of them into a new tab. That covers the conversations no
pane is currently attached to: the one from yesterday, or one whose pane you
closed.

The list is read from the agents' own transcripts, each behind the same small
interface: where a conversation is, what was last said in it, and what this
directory has. Claude Code's implementation is the one that was already here —
the folder is derived from the working directory, and since that mangling is
Claude's business, a folder that does not match is found by reading which
directory its transcripts record. The built-in chat client keeps JSONL of its
own, which resuming one of its panes reads, but the list is Claude Code's alone
for now. An agent that writes nothing Flockdeck can read
contributes nothing to the list, and every caller copes with that rather than
special-casing it. A conversation already open in a pane is shown as such
rather than offered twice, because two panes on one transcript would fight.

### Projects

The rail down the left of the window has a tile for each open project.
Several projects stay open at once and **switching does not stop anything** —
the other project's agents keep working, and its tile turns amber if one of
them starts waiting on you while you are elsewhere.

Opening a folder does not need the command line. The picker offers the projects
you have opened before and a directory browser that flags git repositories, so
a project is two clicks away. (The browser is served by the Go side: a web page
cannot be handed a real directory path.)

Each project keeps its own tabs, its own layout file, its own restored
conversations, and its own default agent and model — so the repository you want
Codex on gets Codex from the plain one-keystroke split, while everything else
goes on getting Claude Code.

### Git worktrees

Worktrees are how you run agents in parallel without them fighting over one
checkout, so they are treated as a first-class part of the app rather than a
list of paths. For every worktree the panel shows:

- the branch, or `detached@abc1234`, and the short commit
- **how many agents are already working in it**
- uncommitted work, split into changed and new files
- how far ahead or behind its upstream it is
- whether it is the main worktree, or locked

From there you can open a worktree as an agent tab, as a shell, or **split it
in beside the pane you are looking at**; create a worktree for a new branch off
any base ref; check out a branch that has no worktree in one click; remove a
worktree (with a confirmation that says what will be discarded when it has
uncommitted work); and prune records left behind by folders deleted outside
git. A worktree whose folder was deleted outside git is marked **folder
gone**: only git's record of it is left, and **Prune** clears it.

Pane headers carry the same information for the checkout they are working in —
branch, `●n` uncommitted files, `↑n`/`↓n` against upstream — refreshed in the
background for the project on screen, so you can see the state of every agent's
tree at a glance; another open project's checkouts are read when you switch to
it and when the Agents overview opens. Work
left uncommitted inside a submodule is counted in the review panel but not in
the header, which would otherwise run git inside every submodule on every
refresh; a submodule moved to another commit is counted in both. Each
checkout is read on its own: one that git does not answer for within ten seconds — a
very large checkout, or one on a network drive gone quiet — holds up no other
pane, and its own headers say *git timed out* in place of counts that may be out
of date, until it answers again.

### Finding your way around

`Ctrl+Shift+K`, or **Commands** in the top bar, opens the command palette. It
lists every action except tab switching, and it switches to any open project
or tab by name. `Ctrl+Shift+F` searches the focused terminal. `Ctrl+=` and
`Ctrl+-` change the terminal font size for every pane, and Flockdeck remembers
it. Settings → Terminal sets the size, typeface, scrollback and cursor.

### Settings

`Ctrl+,`, or **Settings** at the foot of the rail, opens one dialog for
everything Flockdeck remembers: notifications and updates (General), font,
scrollback and cursor (Terminal), the default agent and model, routing and
whether Claude panes read their usage limits (Agents), keys
(API keys), pairing (Remote access), and your plan (Account & plan). Every
control changes its setting at once. Below 640px wide the rail folds into a
menu behind the ☰ button at the left of the top bar, which is how a phone or a
narrow window reaches Settings and the other tools.

### Help

`F1` opens the help: a page per feature, searchable across all of them, beside
a contents list. Almost every dialog carries a `?` that opens the page
explaining what is in it, and a dismissible hint appears under the tab bar for the gestures the
interface cannot advertise for itself — dragging a pane, what an amber dot
means. The first run opens it once, unasked, and never again.

The pages are Markdown under `internal/help/pages`, compiled into the binary
and rendered on the Go side. They do not write shortcuts out by hand: a page
says `[[key:splitRight]]` or `{{keys:Panes}}`, and both are expanded from the
key table in `internal/help/keys.go` that the command palette, the keyboard
dispatch and the table above are also drawn from. A binding therefore changes
in exactly one place, and a page naming an action that no longer exists fails
its test rather than misleading a reader.

### Broadcast input

The prompt bar (`Ctrl+Shift+P`) composes one instruction and sends it as one
submitted message, which is what you want for "run the tests and fix what
breaks" across several worktrees. `Enter` sends it and `Shift+Enter` starts a
new line; a prompt of several lines is handed to each pane as a bracketed
paste where its program has asked for one, so it still arrives as one
message rather than one per line. Broadcast decides who receives it: with it
off, the focused pane and any panes picked with the `⇉` button in their
header; with it on, every pane in the broadcast set — by default every agent
in the tab on screen. What you type into a pane still goes to that pane alone.

### Remote access

The agents are on the desktop; the person is not always at it. Remote access
opens the same window from another device — a laptop, a tablet, a phone —
through a relay, without a VPN and without opening a port on this machine.

```sh
flockdeck remote enable           # enrol this machine with the relay
flockdeck remote pair             # a one-time link, and a QR code of it, for a device
flockdeck remote status           # whether it is on, and whether it is connected
flockdeck remote devices          # what is paired
flockdeck remote revoke <id>      # unpair one, by its id or its name
flockdeck remote rename <name>    # what every paired device calls this machine
flockdeck remote rename -device <id> <name>   # what a paired device is called
flockdeck remote disable          # remove this machine from the relay
```

`remote pair -desktop` prints a code that `remote enable -join <code>` on
another machine uses to join the same account, so one paired device reaches
both. `enable -invite <code>` is for a relay that asks for an invitation, and
`disable -force` forgets the enrolment here when the relay cannot be told.

Flockdeck dials *out* to the relay — `https://remote.flockdeck.ai` unless
`flockdeck remote enable -relay <url>` or `FLOCKDECK_RELAY` names another — and holds one WebSocket open
while it runs, carrying a stream multiplexer. Each connection a paired browser
makes becomes a stream, and each stream is served in-process by the very
handlers the local window uses. So the remote window is not a second interface
kept level with the first: it is the first, every pane and every dialog, and
the front end asks for everything relative to wherever it was served from so
that it works under the relay's per-machine prefix unchanged. A pane open in
two windows at once takes the size of whichever last typed into it or focused
it, so glancing from a phone leaves the desk's terminal alone, and typing on the
phone fits it to the phone until you type at the desk again.

A paired browser opens the relay's own client first, built for a small screen.
It shows which agents are waiting, opens any pane — as a chat with the agent
where Flockdeck can read one, its terminal otherwise — and lets you answer a
question or a permission prompt with a tap. **Full interface**, in its top
bar, opens this window through the same tunnel. The client resizes a pane
only when you ask it to fit the pane to the screen.

### Your agents on your phone

Opening a pane from a paired device shows a chat with the agent, not its raw
terminal, for a Claude Code pane and for Flockdeck's own chat client (the
built-in Anthropic, OpenAI, Google and OpenAI-compatible agents) — anything
else still opens as a terminal, because Flockdeck doesn't yet read what it's
saying. Nothing to turn on: the phone asks the desktop when it opens a pane,
and gets a chat back if there is one to give it. A **Chat**/**Terminal**
switch in the pane's own header moves between the two anyway, and is
remembered there, per device, per pane.

Replies render as Markdown — headings, lists, tables, quotes, and code with a
copy button, a wrap toggle and syntax colouring. Each turn's tool calls and
thinking fold into one line, "12 steps · 3 files edited · 4 commands", tapped
open to see each step; an edit shows its diff, with line numbers. A question
or a permission prompt appears as a card with buttons in the chat, rather
than needing the terminal — multiple choice, a typed answer, and Yes/No for a
permission are all covered, including a call that asks several questions at
once: answer them one at a time on the card, then send them all together.

While an agent works, a **Stop** button — the same as pressing Escape — and
"Working for 3m — Bash", naming what it's doing, sit above the prompt box;
once it's idle, quick replies — Continue, Yes, go ahead, Explain that more
simply, Run the tests — cover the common ones without typing. Every open
pane also appears in the paired device's list with its latest reply, or the
question it's waiting on, a time, and an unread dot, so you can see what's
happened everywhere without opening each one. Messages Flockdeck itself
injects — a background task finishing, a session notice — show as small
notes, never as if you had typed them.

A screenshot in the conversation shows as a thumbnail that opens full
screen; you can attach a photo from the phone too. It's shrunk on the phone
before it's sent, kept on this desktop — in Flockdeck's own folder, never
your project — and removed after about a week; the agent is told its file
path, the same way typing one would tell it.

Most of this needs a fairly recent Flockdeck on the desktop; paired with an
older one, a pane simply opens as a terminal instead. Even where a pane does
open as a chat, a few parts fall back gracefully on a desktop too old to send
them, rather than breaking: no live timer, a plain "waiting for you — open
the terminal to answer" banner instead of a question or permission card, and
no preview text in the paired device's list.

Pairing is a link that works once and expires in minutes, shown as a QR code
by **Remote access…** in the command palette (and the **Remote** button in the
rail) or printed by `flockdeck remote pair`. The
device that opens it can open this window until it is unpaired, from that
dialog or from the command line, which ends its session at once.

A machine or a device is renamed with **Rename** in that dialog, or
`flockdeck remote rename`; a paired device can rename either from its Devices
page. A machine wiped before remote access was turned off on it can no longer
take itself off the relay, so that page removes it too.

What the relay can see is stated plainly: traffic is TLS between the browser
and the relay and between the relay and this machine, and the relay decrypts
it to route it. It is **trusted**, not end-to-end encrypted. It never sees the
local server's token or any API key — a request is let in here because it came
through the tunnel, which only the relay can put one on, and the relay has
already checked the device is paired with the account. The endpoints only
another launch of the binary uses (`-quit`, opening a project from the command
line) still insist on the local token, so no remote window can reach them. The
credential the relay knows this machine by is in `remote.json` in the state
directory, readable only by you.

The shared relay gives every desktop's window its own address —
`https://<desktop id>.d.flockdeck.ai/` — so a page from one desktop's window
cannot reach another's: it runs on a different origin, with none of the
account's own session, and so cannot list your devices, open another desktop,
or make a code for one to join. Unpairing a device or revoking this machine
ends its session at once, on the next request, with nothing left open behind
it: the cookie that a window holds carries no permission of its own, only
which device and desktop it was issued for, and both are checked again every
time. A desktop not heard from in 30 days is removed from the relay on its
own.

A remote window needs Flockdeck running here. Closing the window on this
machine still quits it, remote window or not — detach instead to leave the
agents running for later. A remote window closing never stops anything.

Coming soon, for companies: Enterprise, a licence to run the relay on your own
infrastructure, with SSO and support, for a company whose rules don't allow a
third party to decrypt its developers' terminal traffic.

### Push notifications

A paired phone can be told when an agent has been waiting on you, whether or
not its browser is open on it and whether or not a window is open here — a
run left detached reaches you too. On the phone, open a desktop and press
**Notify me when an agent needs me**. On an iPhone or iPad, add the page to
the Home Screen first — Share, then **Add to Home Screen** — since iOS and
iPadOS send notifications only to web apps added that way, from version 16.4.
Tapping a notification opens the pane that is waiting.

Settings → **Remote access** says what is sent:

- **Notify paired devices** turns notifications off for every device at once;
  a device turns itself off again from its own **Notify me…** toggle.
- **After waiting** is how long an agent has to have been waiting first: 30
  seconds, unless you choose otherwise, from 5 seconds to an hour. Each wait
  is told once, and an agent that is answered and then asks again is a new
  wait. However many agents are waiting, the phone is sent one notification
  that says how many, which replaces the one before it, and no more than one
  a minute. It is sent once an agent has waited that long and nobody has used
  this computer — keyboard or mouse, in any application — for two minutes, or
  its screen is locked; a Flockdeck window being in front of you makes no
  difference. Where the operating system's idle time can't be read, Flockdeck
  falls back to typing and clicks in its own windows instead. Nothing is sent
  about a pane you are using on the phone; if it is still waiting two minutes
  after you leave it, you are told then.
- **Send nothing identifying** has a notification say only "An agent on *this
  machine* needs you", rather than naming the pane and its project — for a
  lock screen others can see.

Each notification is encrypted here, on this machine, for the device it goes
to (Web Push, RFC 8291): the relay only signs it and passes it on, and cannot
read it, nor can the push service that carries it — Apple's, Google's,
Mozilla's or Microsoft's, whichever the browser uses. Every notification is
the same size, however long the names in it, so even that leaks nothing.
Whether you are at your computer is worked out here, on this machine, and
never sent anywhere.

## Design notes

- **Real PTYs.** Every pane is a genuine pseudo-terminal
  ([`go-pty`](https://github.com/aymanbagabas/go-pty), ConPTY on Windows).
  Fidelity comes from running the CLI in a terminal, not from parsing its
  output.
- **An agent is data, not a branch.** Which program to run, which models it
  offers, how its status is known, where its transcript is and how its briefing
  reaches it are fields on one struct. Adding an agent is a table entry, and
  for a user it is a few lines of JSON — which is the only reason a second
  agent did not become a second copy of every feature.
- **The API agent is the same binary.** Talking to a model API directly is a
  subcommand run in the pane's own terminal, reporting the same lifecycle
  events over the same loopback endpoint as a CLI agent's hooks. Nothing in the
  workspace learns a second protocol, and there is still one program to
  install; on Windows it also ships as `flockdeck-chat.exe`, linked for the
  console, because the window build gets none in a pane.
- **Emulation happens in the browser.** xterm.js renders the terminal and
  encodes keystrokes for whatever modes the application has enabled, so raw
  bytes pass through Go untouched in both directions. Go never has to
  reimplement a terminal.
- **Panes survive a reload.** Each session keeps a bounded ring buffer of recent
  output; a window that connects, reconnects or reloads replays it into a fresh
  terminal. A viewer that stops reading is dropped rather than being allowed to
  stall the process feeding it.
- **The workspace has a single owner.** It is reached from many connection
  goroutines, so every read and write of it is funnelled through one goroutine.
  Slow work — git, reading transcripts — runs outside that loop and only its
  results are applied there, so a fan-out creating five worktrees does not
  freeze the window.
- **Git is read in bulk, off the hot path.** One `status --porcelain=v2
  --branch` per checkout gives branch, upstream, ahead/behind and file counts
  together, worktrees are examined concurrently, and pane summaries refresh on
  a timer rather than per frame.

## Development

```sh
make check     # go vet + tests
make race      # tests under the race detector (needs a C toolchain)
```

The test suite covers the layout tree, the hook transport, the output ring and
bell detection, layout persistence, and the server end to end — including a
full terminal round trip where a keystroke sent over a WebSocket reaches the
process and its output comes back. It also holds the documentation to the
code: every action in the key table has to be implemented in the front end and
listed in the help, no binding may be written into the front end by hand, and
the README table above has to match the key table.

To add an agent, add a `Spec` to the built-in catalog under `internal/agent`.
Verify its flags against the tool itself before you do: an entry that claims a
resume flag the CLI does not have is worse than one that claims nothing, and an
agent with no declared capabilities still runs perfectly well as a terminal
with a program in it.

To add a help page, write `internal/help/pages/<slug>.md` — starting with an
`#` heading and a paragraph of summary, which the contents list takes — and add
its slug to `order` in `internal/help/help.go`. To change a shortcut, edit
`internal/help/keys.go` and run:

```sh
go test ./internal/help -run TestREADMEShortcuts -update
```

Two programs under `cmd/` make what is published rather than the application:

- `cmd/release` cross-builds every platform and writes the archives and
  `checksums.txt` a release is made of; `make package` runs it.
- `cmd/sitegen` writes the landing page at flockdeck.ai, and the install
  scripts it serves, from `cmd/sitegen/assets`:
  `go run ./cmd/sitegen -out ../flockdeck-site -release v1.2.3 -checksums checksums.txt`,
  where `checksums.txt` is that release's, from dl.flockdeck.ai, with its
  `checksums.txt.sig` beside it. sitegen checks that signature against the
  release key built into it, and writes nothing unless it is the key's. The
  install scripts install that release and check its archive against the
  SHA-256 written into them.

Four development aids live under `cmd/` and are not part of the product:

- `cmd/hooktest` starts one real agent pane, sends it a prompt and prints every
  status transition, verifying the hook pipeline end to end. Build the binary
  first and point it there — `go run ./cmd/hooktest -hookbin ./flockdeck.exe`.
  The prompt is a real one, so it spends a short turn through your own Claude
  Code login.
- `cmd/ctl` drives a running instance over its control socket, `cmd/statedump`
  prints what it reports about its panes, and `cmd/treedump` prints the tab and
  split structure. Together they are how the UI is exercised and inspected
  without clicking — a rearrangement can be sent and the resulting tree read
  back.

```sh
go build -o flockdeck.exe . && ./flockdeck.exe -solo -no-window
go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"cmd":"splitPane","dir":"h","kind":"agent","agent":"claude"}'
```

The front end is vendored, not fetched at build time. To update it, replace the
files in `internal/webui/assets/vendor/` from the `@xterm/xterm`,
`@xterm/addon-fit`, `@xterm/addon-search` and `@xterm/addon-webgl` packages.

## Limitations

- An agent that reports nothing about its own lifecycle is watched from its
  terminal instead, so its status is a guess and can be a beat behind. Status
  is a fact only for the agents that report one — Claude Code, and the built-in
  API client.
- Spend figures are estimates, kept in memory only, and only Claude Code and
  the built-in API agents report them. There is no history of them yet, by
  day or by month.
- A commit from the Changes panel takes every file in its list, whole; there is
  no selective staging, and a shell pane is the answer for anything finer. A
  commit is refused when the tree has moved since you looked, but a file
  written to again is noticed by its size and modification time, not its
  content, so an edit that changes neither goes through.
- The window needs a browser engine present. Every supported platform ships one
  or has one in practice, but on a bare Linux install with no browser at all
  there is nothing to display the interface in.
- Release binaries are not signed. The install scripts are unaffected, but a
  copy downloaded in a browser, from dl.flockdeck.ai or GitHub, can be stopped
  by Gatekeeper or SmartScreen the first time it runs; the in-app help's
  troubleshooting page says how to let it through.

## Licence

The desktop app in this repository is MIT — see [LICENSE](LICENSE). The relay,
the phone and web client, and the website are separate, closed-source
projects, © 2026 Jim Wright, all rights reserved.

The front end is compiled into the binary and the Go dependencies are linked
into it, so a release carries other people's code as well as this project's.
All of it is permissive (MIT, ISC and BSD 3-Clause) with no copyleft anywhere,
and the notices each of those licences asks for are in
[THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md), which ships inside every
release archive beside the binary.

The agents Flockdeck runs are separate programs and are not redistributed with
it. Claude Code, Codex, Gemini CLI, Aider, opencode and Cursor's agent each
keep their own licences and terms, as do the model APIs the built-in client
talks to.
