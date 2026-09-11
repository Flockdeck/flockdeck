# Knowing who needs you

Running one agent is easy. Running six is not: they finish at different times
and they block on permission prompts, and the one that is waiting is rarely
the one you are looking at.

## The dots

Every pane header carries a status dot.

| Dot | Meaning |
| --- | --- |
| Green, pulsing | **Working** — producing output or running a tool |
| Amber | **Waiting on you** — a permission prompt or a question |
| Grey | **Idle** — it finished its turn and is ready for a new prompt |
| Red | **Exited** — the process is gone |

While a tool is running the pane header names it, so "Reading", "Bash" or
"Edit" tells you what the agent is actually doing rather than only that it is
busy.

## Where else it shows

- **The tab** holding a waiting agent is marked `▲`.
- **The window title** reports the count, so a waiting agent is visible in the
  taskbar with the window behind something else.
- **The project chip**, at the left of the tab bar, turns amber when an agent
  in a project you are *not* looking at starts waiting.
- **A desktop notification** is raised when an agent blocks while the window is
  not in front. It is not raised when you are already looking at the window —
  the tab marker is enough, and a toast would be noise.
- [[key:agents]] lists every pane in every open project with its status, and
  jumps to any of them.

## Where it comes from

For an agent that can report its own lifecycle, this is not screen scraping.
Claude Code is launched with a generated `--settings` file registering its
lifecycle hooks — `UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop` and
the rest — and an API agent, which is Flockdeck's own chat client, reports the same
events itself. Either way the event re-invokes this same binary in a hidden
mode, which reports it to the application over the loopback interface.

Status therefore reflects what the agent is actually doing rather than what
its output happens to look like. Those settings are additive: your own
settings, hooks and permissions still apply.

## An agent that cannot report

Not every coding agent has a lifecycle to report, and Flockdeck runs those too. For
their panes the status is read from the terminal instead: the bell an agent
rings when it wants you, a quiet timer for when it has stopped producing
output, and the lines it prints — the shape of a permission question, the shape
of a prompt waiting to be typed at. Only the last few hundred bytes are looked
at, with the escape sequences stripped, so a question two screens back does not
keep a finished pane amber.

That is a guess where the other is a fact, and it is worth knowing which you
are looking at: **Agents and models** says which agents report and which are
read. Where both exist, a reported event always wins.
