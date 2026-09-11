# Flockdeck

A desktop application for running several coding agents at once.

Each agent runs in a real pseudo-terminal, so it behaves exactly as it does in
a normal terminal — permission prompts, slash commands, plan mode, colours,
mouse. Around them the app adds what you need to run six at a time: tabs, split
panes, per-agent status, layout persistence, git worktrees and broadcast input.
Each agent is told which pane it is and who else is working, so being one of
several is something it can act on.

The agent and the model are chosen per pane. Claude Code is the default and
nothing about it changed; beside it Flockdeck will run Codex, Gemini, Aider,
opencode or Cursor's agent, and it will talk to a model API directly — its own
chat client in the pane, no wrapper CLI, no node, no Python — including a local
Ollama or any other OpenAI-compatible endpoint.

A CLI agent uses whatever login it already has, so Claude Code panes need no
key and no separate account. Talking to an API directly needs one, and `flockdeck
keys` keeps it.

```
┌ flockdeck ──────────────────────────────────────── ─ □ × ┐
│ [api ▾] │ [ main ] [ fix-auth ▲ ] +  Broadcast Worktrees │
├───────────────────────────┬──────────────────────────────┤
│ ● api ⎇ main ●3  Reading  │ ▲ api ⎇ fix-auth ↑2          │
│   claude · sonnet         │   codex · gpt-5              │
│  (live agent terminal)    │  (live agent terminal)       │
├───────────────────────────┴──────────────────────────────┤
│ ○ shell ⎇ main                                           │
└──────────────────────────────────────────────────────────┘
   ▲ = blocked on you   ●3 = uncommitted files   ↑2 = ahead
```

## What it gives you

- **Several agents at once**, each in a real terminal, in tabs and split panes.
- **Any agent, any model, per pane** — a CLI you already have, or a model API
  spoken to directly by the binary itself, picked per pane and remembered per
  project.
- **Rearrange what is already running** — drag a pane to another edge, another
  tab or a tab of its own, or merge two tabs into one, without restarting the
  agent in any of them.
- **Each agent knows where it is** — its own conversation, its own checkout, and
  a briefing at session start on which pane it is and who else is working.
- **One glance tells you who needs you** — per-pane status driven by the agent's
  own lifecycle where it reports one, tab and project markers, and a desktop
  notification when an agent blocks while you are looking elsewhere.
- **Multiple projects open together**, switched without stopping anything.
- **Worktrees as a first-class thing**: create, inspect, occupy and remove them
  without leaving the app.
- **Everything comes back**: layouts, the set of projects you had open, and
  each pane's conversation.
- **Agents can outlive the window** — detach, close it, reattach later.
- **One agent's plan becomes several agents doing the work**, each in its own
  git worktree.
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
go install github.com/jmwri/flockdeck@latest
```

Or from a clone:

```sh
make build      # a binary for this machine
make dist       # binaries for all six supported platforms
```

The whole program builds with `CGO_ENABLED=0`, including the PTY layer and the
front end, so `windows`, `linux` and `darwin` on both `amd64` and `arm64` all
cross-compile from any one machine with nothing but the Go toolchain. There is
one artifact: a single binary with no assets to install beside it.

### Staying up to date

Every release is published on GitHub as one archive per platform — a `.zip` for
Windows, a `.tar.gz` elsewhere — with a `checksums.txt` beside them. Tagging a
commit `v1.2.3` is the whole of cutting one: the workflow vets, tests,
cross-builds all six and publishes them.

A running Flockdeck watches for releases and downloads anything newer in the
background, checking it against its published SHA-256. Nothing is replaced
while you are working. When a release is ready a chip appears in the top bar,
and installing it is a restart you ask for: the layout is saved and reopened,
though the agents running in panes are stopped, which is why it is never done
for you.

From a terminal:

```sh
flockdeck update          # fetch the latest release and put it in place
flockdeck update -check   # say whether there is one, and stop
```

Replacing the binary leaves a running instance alone — it is already loaded —
so the new version is what starts next time. A build you made yourself is
stamped `dev` and is never replaced by a release. `FLOCKDECK_UPDATE=off` turns
the background check off; the subcommand still works.

## Running it

Double-click the binary, launch it from a shortcut, or run it from a terminal —
all three work. It opens its own window; there is no terminal to keep around.

```sh
flockdeck                 # open the current directory
flockdeck -C ~/code/api   # …or attach to a running instance and open it there
flockdeck -new            # ignore the saved layout
flockdeck -shell          # first pane is a shell, not an agent
flockdeck -detach         # run with no window; attach to it later
flockdeck -quit           # stop a running instance and its agents
flockdeck -no-window      # just serve; print the URL and open it yourself
flockdeck -solo           # start a separate instance instead of attaching

flockdeck keys set openai # give an API agent a key, read from stdin
flockdeck keys list       # which agents have one, not what it is
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
Brave or Chromium, whichever is found first. On Windows this is always
satisfied because Edge ships with the OS. If none is installed the page opens
as an ordinary tab in your default browser instead, which works but looks less
like an application. `FLOCKDECK_BROWSER` forces a specific one.

Nothing is exposed to the network: the server binds to `127.0.0.1` on a random
port and every request — page, assets and both WebSockets — must carry a token
generated fresh for each run.

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

### Agents

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+B` | Toggle broadcast |
| `Ctrl+Shift+P` | Prompt all panes |
| `Ctrl+Shift+X` | Fan out — turn this pane's plan into agents |
| `Ctrl+Shift+A` | All agents across projects |

### Git

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+G` | Worktrees |
| `Ctrl+Shift+S` | Review changes, commit and push |

### Finding your way

| Keys | Action |
| --- | --- |
| `Ctrl+Shift+K` | Command palette |
| `Ctrl+Shift+F` | Find in terminal |
| `Ctrl+Shift+R` | Resume a past conversation |
| `Ctrl+Shift+O` | Projects |
| `F1` | Help |

### The window

| Keys | Action |
| --- | --- |
| `Ctrl+=` | Increase font size |
| `Ctrl+-` | Decrease font size |
| `Ctrl+0` | Reset font size |
| Command palette | Detach — close the window, leave agents running |
| Command palette | Quit — stop every agent in every project |

<!-- shortcuts:end -->

Panes are focused by clicking, resized by dragging the divider between them,
moved by dragging their header, and closed, restarted or zoomed from the
buttons in their header.

## How each feature works

### Agents and models

A pane is a shell or an agent, and an agent is two choices: which agent, and
which model. Both are made per pane, defaulted per project, and remembered with
the layout.

The plain split and new-tab keystrokes take the project's default and stay one
keystroke, because that is what you want almost every time. The picker — held
split button, the caret beside **New tab**, or the command palette — is for
choosing deliberately. It lists agents as **installed** and **not installed**,
each expanding to its models with the default marked, and offers *set as
default for this project* at the foot. An agent you have not got is greyed with
where to get it rather than hidden: somebody who has never installed Codex
should still learn that Flockdeck would run it. The pane header then names what it
got beside the branch, in the same dim weight — `claude · sonnet`,
`codex · gpt-5`.

There are two ways an agent gets run:

- **A CLI**, started in the pane's pseudo-terminal exactly as you would start
  it yourself, using whatever login it already has. Claude Code, Codex, Gemini,
  Aider, opencode and Cursor's agent are built in.
- **An API, spoken to directly.** `flockdeck chat` is Flockdeck's own terminal chat
  client, run in the pane, talking straight to a model API: Anthropic, OpenAI,
  Google, and any OpenAI-compatible endpoint, which is how a local Ollama, LM
  Studio or vLLM — or a gateway — becomes an agent. No wrapper CLI, no node, no
  Python; one binary. It streams, it renders code and tool calls, it carries the
  model and the running cost on a status line, and it has the file and command
  tools an agent needs, each confined to the pane's working directory and each
  asking before it writes or runs anything. That ask is reported as a
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
variables first and then from `keys.json` in the state directory, written by
`flockdeck keys set <agent>` reading stdin. A key reaches exactly one place — the
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
| red | The process exited |

For an agent that reports its own lifecycle, this is not screen scraping. A
Claude Code pane is launched with a generated `--settings` file registering its
lifecycle hooks (`UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop`, …),
and an API pane's chat client reports the same event names itself, so nothing
in the workspace has to learn a second protocol. Either way the event re-invokes
this same binary in a hidden `hook` mode, which posts it to a loopback server
the app runs, authenticated with a per-run token. Status therefore reflects
what the agent is actually doing rather than what its output happens to look
like, and `PreToolUse` even surfaces the running tool's name in the pane header.

The settings are additive — your own settings, hooks and permissions still
apply.

Not every coding agent has a lifecycle to register, and Flockdeck runs those too.
For them the status is read from the terminal instead: the bell, a quiet timer,
and per-agent patterns for the two lines that matter — the shape of a
permission question, and the shape of a prompt waiting to be typed at. It is
matched against the last few hundred bytes with the escape sequences stripped,
never against the whole scrollback, and a reported event always beats a
pattern. It is a guess where the other is a fact, and the help says which
agents are which so you know which you are looking at.

### Rearranging what is already running

A layout is rarely right first time: the agent you thought was a side errand
turns out to be the one you are watching, and it is in the wrong corner. Panes
are therefore movable, not just creatable.

**Drag a pane by its header.** While a drag is in progress every other pane
shows where the pane would land:

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

So every agent pane is told, in its own words, when it starts:

- which pane it is, in which tab and which project;
- the directory and branch it has, and whether that is a worktree of its own
  rather than the project root;
- what it was spawned to do, when it was started by a fan-out or by another
  agent rather than by hand;
- which other agents are running beside it, where each of them is working,
  which agent and model each of them is — `codex · gpt-5`, because what is in
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

An agent with no hook to answer gets the same briefing in front of its opening
prompt instead, fenced in a `<flockdeck-context>` block so it can tell the two
apart — and gets it alone if there is no opening task. That is once per launch
rather than once per compaction, which is honest as long as the briefing says
when it was taken, and it does.

Panes also carry `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME`, `FLOCKDECK_PROJECT`,
`FLOCKDECK_AGENT` and `FLOCKDECK_MODEL` in their environment. The first three are what
a shell pane — with no lifecycle hooks of its own — has to go on; the last two
are how a script or a prompt can say what it is sitting in. The first three
were `PERCH_*` before the rename; both spellings are set for now, so a
shell prompt written against the old names keeps working until a later release
drops them.

The isolation this describes is real rather than advisory. Each pane is a
separate top-level session with its own session id, its own generated settings
file, and an environment scrubbed of the markers a parent agent session would
otherwise pass down. Those markers are stripped for every agent in the catalog
rather than only the one in this pane, because Flockdeck may have been launched
from inside any of them; nothing is shared between two panes.

### Session persistence

Layouts are saved per project. On the next run the tabs, splits, proportions
and working directories come back, **every project you had open is reopened**,
each pane starts the agent and model it had, and each one resumes the
conversation it had before rather than starting an empty one. The project you
name on the command line is the one you land in; the rest are restored around
it.

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

What it reads is the agent's own transcript, not the pane's screen. The screen
is a redrawn interface: bullets are wrapped to the pane's width and so cut
mid-sentence, the status line begins with a glyph indistinguishable from a
bullet, and the agent's thinking sits in the same column as its answer — all of
of which arrives looking like a plan. The transcript is the markdown the agent
actually wrote. A pane with no transcript Flockdeck can read — a shell, or an agent
that keeps none — still falls back to the screen.

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

The dialog carries one agent-and-model control for the whole run and an
override on each row, so twelve tasks can be split between two agents
deliberately — the capable one for the refactor, the cheap one for the six
renames.

The task is handed to the agent as its opening argument rather than typed into
the terminal, so it is submitted the moment the agent starts rather than
depending on guessing when the interface is ready.

There is one wrinkle worth knowing about, and the dialog handles it: a fresh
worktree is a directory the agent has never seen, and an agent with a trust
question of its own — Claude Code has one — would stop and ask whether the
folder is trusted before doing any work, once per child. If the project you are
fanning out from is already trusted, the dialog offers to carry that same
answer over to the worktrees it creates. It is a checkbox, it says what it
does, it is not offered for an agent that has nothing to ask, and it will not
invent trust: inheriting is refused unless the source directory is genuinely
trusted already.

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

### Review, commit and push

`Ctrl+Shift+S` shows what changed in the working tree an agent has been using:
each file with what happened to it and how many lines moved, a coloured diff of
whichever file you select, and the branch's position against its upstream.
From there you can commit, commit and push, pull or fetch. The first push sets
the upstream, so a branch a fan-out invented does not need a hand-typed command
to leave the machine.

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
own and reads it the same way. An agent that writes nothing Flockdeck can read
contributes nothing to the list, and every caller copes with that rather than
special-casing it. A conversation already open in a pane is shown as such
rather than offered twice, because two panes on one transcript would fight.

### Projects

The button on the left of the tab bar switches projects and opens new ones.
Several projects stay open at once and **switching does not stop anything** —
the other project's agents keep working, and its marker turns amber if one of
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
git.

Pane headers carry the same information for the checkout they are working in —
branch, `●n` uncommitted files, `↑n`/`↓n` against upstream — refreshed in the
background, so you can see the state of every agent's tree at a glance.

### Finding your way around

`Ctrl+Shift+K` opens a command palette listing every action, including
switching to any open project or tab by name. `Ctrl+Shift+F` searches the
focused terminal. `Ctrl+=` and `Ctrl+-` change the terminal font size, which is
remembered.

### Help

`F1` opens the help: a page per feature, searchable across all of them, beside
a contents list. Every dialog carries a `?` that opens the page explaining what
is in it, and a dismissible hint appears under the tab bar for the gestures the
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

Broadcast mirrors your typing into every pane in the broadcast set — by default
every agent in the current tab, adjustable with the `⇉` button in each pane
header. The prompt bar (`Ctrl+Shift+P`) composes one instruction and sends it to
all of them at once, which is what you want for "run the tests and fix what
breaks" across several worktrees.

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
  workspace learns a second protocol, and there is still one artifact to
  install.
- **Emulation happens in the browser.** xterm.js renders the terminal and
  encodes keystrokes for whatever modes the application has enabled, so raw
  bytes pass through Go untouched in both directions. Go never has to
  reimplement a terminal.
- **Panes survive a reload.** Each session keeps a bounded ring buffer of recent
  output; a window that connects, reconnects or reloads replays it into a fresh
  terminal. A viewer that stops reading is dropped rather than being allowed to
  stall the process feeding it.
- **The workspace has a single owner.** It is reached from many connection
  goroutines, so every read and write of it is funnelled through one goroutine;
  slow work such as git runs outside that loop and only its results are applied
  there.
- **Slow work never blocks the state.** Git and transcript reading happen off
  the goroutine that owns the workspace, and only their results are applied
  there — a fan-out creating five worktrees does not freeze the window.
- **Git is read in bulk, off the hot path.** One `status --porcelain=v2
  --branch` per checkout gives branch, upstream, ahead/behind and file counts
  together, worktrees are examined concurrently, and pane summaries refresh on
  a timer rather than per frame.
- **Panes get a clean environment.** An instance launched from inside Claude
  Code would otherwise leak `CLAUDE_CODE_CHILD_SESSION` and friends into every
  pane, making each behave as a nested child session. Those markers are
  stripped — every agent's, from every pane, because Flockdeck may have been
  launched from inside any of them.

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

Two development aids live under `cmd/` and are not part of the product:

- `cmd/hooktest` starts one real agent pane, sends it a prompt and prints every
  status transition, verifying the hook pipeline end to end.
- `cmd/ctl` drives a running instance over its control socket, `cmd/statedump`
  prints what it reports about its panes, and `cmd/treedump` prints the tab and
  split structure. Together they are how the UI is exercised and inspected
  without clicking — a rearrangement can be sent and the resulting tree read
  back.

```sh
go build -o flockdeck.exe . && ./flockdeck.exe -no-window
go run ./cmd/ctl "ws://127.0.0.1:PORT/ws/control?t=TOKEN" '{"cmd":"splitPane","dir":"h","kind":"agent","agent":"claude"}'
```

The front end is vendored, not fetched at build time. To update it, replace the
files in `internal/webui/assets/vendor/` from the `@xterm/xterm`,
`@xterm/addon-fit` and `@xterm/addon-webgl` packages.

## Limitations

- An agent that reports nothing about its own lifecycle is watched from its
  terminal instead, so its status is a guess and can be a beat behind. Status
  is a fact only for the agents that report one — Claude Code, and the built-in
  API client.
- Commits take the working tree as it stands; there is no selective staging in
  the Changes panel. A shell pane is the answer for anything finer.
- The window needs a browser engine present. Every supported platform ships one
  or has one in practice, but on a bare Linux install with no browser at all
  there is nothing to display the interface in.
