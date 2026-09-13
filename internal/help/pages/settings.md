# Settings

[[key:settings]], or **Settings** at the foot of the rail, opens the settings:
the sections on the left and the one you are in on the right. Every control
changes its setting at once, and it is the same setting the command palette and
the keys change, so whichever you use, the others show it.

- **General** — desktop notifications, and whether Flockdeck checks for new
  releases. **Show them again** brings back every hint you sent away.
- **Terminal** — the font size, the font, how many lines each pane keeps to
  scroll back through, and the cursor's shape and whether it blinks, with a
  preview. The size is also [[key:fontUp]], [[key:fontDown]] and
  [[key:fontReset]].
- **Agents** — the agent and model a pane starts when nobody chooses: for every
  project, and for the project on screen, which can go back to **Same as every
  project**.
  - **Routing** — whether a fan-out's rows come with a model chosen for the
    work: **Off**, which it is until you change it, **Suggest** or
    **Automatic**, for every project and for this one. **Never go below** keeps
    it off the smaller tiers. The rules in force are listed, with where
    `agents.json` is, since that is where they are edited, and **Clear routing
    history** empties the record of what it chose. [Agents and models](#agents)
    has the rest.
  - **Claude Code's usage limits** — which Claude panes read their five-hour
    and weekly limits for the pane header: **Only where I have a status line**,
    the default, **Always** or **Never**. It applies to a pane when it starts.
    [Spend and limits](#spend) says what each one costs.
- **API keys** — set, replace or clear the key each API agent uses. A key is
  never shown once it is set.
- **Remote access** — turn it on or off, the relay it goes through, this
  machine's name, and the paired devices.
- **Account & plan** — the free plan you are on, and Enterprise, coming for
  companies to run the relay on their own infrastructure with SSO and support.
  At its foot are the privacy policy and terms for the shared relay, and the
  licences.

Type into **Find a setting** to narrow the sections to the ones that mention a
word. On a narrow screen the sections go across the top.

## Elsewhere in the window

- **The agent and model a pane runs** — [[action:splitRightChoose]] or
  [[action:newAgentTabChoose]]. Kept with the layout.
- **Which agent and model a project starts** — **Agents** in the settings, or
  **Set as default for** at the foot of the picker. Kept in
  `agents.json`.
- **Whether a fan-out's models are routed, and by which rules** — **Agents ›
  Routing** in the settings for the mode and the floor; the rules themselves
  are edited by hand. Kept in `agents.json`.
- **Whether Claude panes read their usage limits** — **Agents › Claude Code's
  usage limits** in the settings. Kept in `prefs.json`.
- **An API agent's key** — **API keys** in the settings, [[action:apiKeys]], or
  `flockdeck keys set <agent>`. Kept in `keys.json`.
- **A tab's name** — double-click the tab, press F2 while the keyboard is on
  it, or **Rename this tab…** in the command palette. An empty name, or **Use the automatic title** there, goes
  back to the title the tab gives itself. Kept with the layout.
- **Which panes the prompt bar reaches** — [[key:toggleBroadcast]], and the `⇉`
  button in each pane header; [Broadcast and the prompt bar](#broadcast) has the
  rest. Kept until Flockdeck stops.
- **The terminal's font, size, scrollback and cursor** — **Terminal** in the
  settings, or the palette's entries for each. Kept in `prefs.json`.
- **This machine's name and paired devices** — **Remote access** in the
  settings, or [[action:remote]]. Use **Pair a device**, **Rename** and
  **Unpair**. Kept on the relay; [Remote access](#remote) has the rest.
- **Desktop notifications** — **General** in the settings, or **Turn desktop
  notifications off** in the palette; kept in `prefs.json`. The browser also
  asks once whether this window may show them, and keeps the answer for that
  run only: each run has an address of its own.
- **Hints under the tab bar** — their close button sends one away for good.
  Kept in `prefs.json`.
- **Theme** — there is only the one, dark. It does not follow the system's
  light or dark mode.

## In the state directory

Everything Flockdeck keeps is in one directory: `%AppData%\flockdeck` on
Windows, `~/Library/Application Support/flockdeck` on macOS, and
`~/.config/flockdeck` on Linux (or `$XDG_CONFIG_HOME/flockdeck` where that is
set).

- `agents.json` — your own agents, changes to the built-in ones, the default
  agent and model, and the routing policy: for every project under `defaults`
  and `routing`, for one under `projects`. [Agents and models](#agents)
  describes it. It is read again each time the picker opens, so editing it
  needs no restart.
- `keys.json` — API keys set through Flockdeck. A key exported in the
  environment is used first.
- `remote.json` — this machine's enrolment with the relay.
  `flockdeck remote disable` removes it.
- `routing.jsonl` — what routing chose for each routed fan-out row, and
  whether you kept it: the rule's name and the models, never the task. It
  keeps the last 10,000 lines, and **Clear routing history** empties it.
- `prefs.json` — the settings under General and Terminal, Claude Code's usage
  limits, whether the help has
  been opened, and which hints were dismissed. Delete it while Flockdeck is not
  running to have the help open on the next start, every hint back, and every
  one of those settings as it first was.
- `layout-….json` — one per project: its tabs, splits and panes.
  `flockdeck -new` starts without it.
- `projects.json` — the recent projects the picker offers.
- `session.json` — which projects are reopened on the next start.
- `error.log` — why Flockdeck failed to start, when it had no terminal to say
  so in.
- `instance.json` — the address of the Flockdeck that is running, so a second
  launch joins it.
- `updates` — a downloaded release waiting to go in when Flockdeck next quits.
- `sessions` — the settings file written for each pane whose agent reports its
  status, which also carries the pane's status line where that goes through
  Flockdeck.
- `chats` — the built-in chat client's conversations.
- `window` — the browser profile the window runs in. On Windows it is kept
  apart from the rest, in `%LOCALAPPDATA%\flockdeck\window`.

To uninstall Flockdeck, quit it and delete the `flockdeck` binary (on Windows,
the `%LOCALAPPDATA%\Programs\flockdeck` folder the installer made, its Start
menu shortcut and its entry in your PATH), then this directory; turn remote
access off first if it is on, so the relay forgets the
machine. Worktrees it made are ordinary git worktrees, and stay beside their
repositories until you remove them.

## In the environment

These are read when Flockdeck starts, so set one where it will be seen then —
`setx NAME value` on Windows, or an `export` line in your shell's profile
elsewhere — and start Flockdeck again.

- `FLOCKDECK_UPDATE` — `off` stops updating in the background, whatever
  **Check for updates** in the settings says: no checks for new releases, and
  nothing already downloaded is put in place. That is what
  keeps an older version you installed on purpose from updating itself to the
  latest.
- `FLOCKDECK_BROWSER` — which browser provides the window, by name or path.
- `FLOCKDECK_RELAY` — which relay `flockdeck remote enable` uses.
- `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and `GEMINI_API_KEY` (or
  `GOOGLE_API_KEY`) — keys for the API agents, used before anything in
  `keys.json`. `FLOCKDECK_API_KEY` is tried after them, for any API agent.
- `CLAUDE_CONFIG_DIR` — where Claude Code keeps its own files; Flockdeck follows
  it to find conversations and folder trust.
- `NO_COLOR` — the built-in chat client draws without colour.
- `FLOCKDECK_COLUMNS`, then `COLUMNS` — how wide the built-in chat client wraps
  its answers; 80 when neither is set.
- `SHELL` — the shell a shell pane runs, as a login shell; `/bin/sh` if it is
  unset or names a program that is not there. On Windows a shell pane runs
  `pwsh` if it is installed, then the shell `COMSPEC` names, then Windows
  PowerShell.

The variables Flockdeck sets in each pane, and the flags that shape one run —
`-agent`, `-new`, `-shell` — are on [The command line](#cli).
