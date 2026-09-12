# Settings

Flockdeck has no settings screen: each thing you can change is changed where it
is used, and kept in one of a handful of files. This page lists every one of
them, how to change it, and where it is kept.

## In the window

- **The agent and model a pane runs** — [[action:splitRightChoose]] or
  [[action:newAgentTabChoose]]. Kept with the layout.
- **Which agent and model a project starts** — **Set as default for this
  project**, at the foot of the picker. To go back to the default for every
  project, remove the project's entry under `projects` in `agents.json`. Kept
  in `agents.json`.
- **An API agent's key** — [[action:apiKeys]], or `flockdeck keys set <agent>`.
  Kept in `keys.json`.
- **A tab's name** — double-click the tab. Kept with the layout.
- **Which panes the prompt bar reaches** — [[key:toggleBroadcast]], and the `⇉`
  button in each pane header. Kept until Flockdeck stops.
- **Terminal font size** — [[key:fontUp]], [[key:fontDown]], [[key:fontReset]].
  Kept until Flockdeck is next started.
- **Paired devices** — [[action:remote]]: **Pair a device** and **Unpair**.
  Kept on the relay.
- **Desktop notifications** — allow them when the window first asks. The
  browser keeps the answer for that run only: each run has an address of its
  own, so the question comes back on the next start.
- **Hints under the tab bar** — their close button sends one away for good.
  Kept in `prefs.json`.
- **Theme** — there is only the one, dark. It does not follow the system's
  light or dark mode.

## In the state directory

Everything Flockdeck keeps is in one directory: `%AppData%\flockdeck` on
Windows, `~/Library/Application Support/flockdeck` on macOS, and
`~/.config/flockdeck` on Linux (or `$XDG_CONFIG_HOME/flockdeck` where that is
set).

- `agents.json` — your own agents, changes to the built-in ones, and the
  default agent and model: for every project under `defaults`, for one under
  `projects`. [Agents and models](#agents) describes it. It is read again each
  time the picker opens, so editing it needs no restart.
- `keys.json` — API keys set through Flockdeck. A key exported in the
  environment is used first.
- `remote.json` — this machine's enrolment with the relay.
  `flockdeck remote disable` removes it.
- `prefs.json` — whether the help has been opened, and which hints were
  dismissed. Delete it while Flockdeck is not running to have the help open on
  the next start and every hint back.
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
  status.
- `chats` — the built-in chat client's conversations.
- `window` — the browser profile the window runs in.

To uninstall Flockdeck, quit it and delete the `flockdeck` binary, then this
directory; turn remote access off first if it is on, so the relay forgets the
machine. Worktrees it made are ordinary git worktrees, and stay beside their
repositories until you remove them.

## In the environment

- `FLOCKDECK_UPDATE` — `off` stops the background check for new releases, which
  is what keeps an older version you installed on purpose from updating itself
  to the latest.
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
  unset. On Windows a shell pane runs `pwsh` if it is installed, then the shell
  `COMSPEC` names, then Windows PowerShell.

The variables Flockdeck sets in each pane, and the flags that shape one run —
`-agent`, `-new`, `-shell` — are on [The command line](#cli).
