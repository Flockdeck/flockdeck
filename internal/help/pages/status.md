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
| Faint grey | **Starting** — launched, and not heard from yet |
| Red | **Exited** — the process is gone |

While a tool is running the pane header names it, so `Read`, `Bash` or `Edit`
tells you what the agent is actually doing rather than only that it is busy.

## Where else it shows

- **The tab** holding a waiting agent is marked `▲`.
- **The window title** reports the count, so a waiting agent is visible in the
  taskbar with the window behind something else.
- **The top bar** counts the agents waiting and working in this project. Click
  the count for the same list [[key:agents]] opens.
- **The rail** down the left of the window has a tile for each open project,
  and the tile of a project with an agent waiting on you carries an amber
  badge — so one that starts waiting in a project you are *not* looking at is
  seen from the one you are.
- **A desktop notification** is raised when an agent blocks while the window is
  not in front. It is not raised when you are already looking at the window —
  the tab marker is enough, and a toast would be noise.
- Claude Code's own idle nudge — sent about a minute after a pane has simply
  gone quiet, waiting for a new prompt — never raises any of the above, for
  any pane: it says nothing more than that the pane is still there. A
  permission prompt or a real question always still reaches you, including
  for **a helper another agent started** for itself (see [[key:fanout]]).
- [[key:agents]] lists every pane in every open project with its status, and
  jumps to any of them.

## Where it comes from

For an agent that can report its own lifecycle, this is not screen scraping.
Claude Code is launched with a generated `--settings` file registering its
lifecycle hooks — `UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop` and
the rest — and an API agent, which is Flockdeck's own chat client, reports the same
events itself. For Claude Code each event re-invokes this same binary in a
hidden mode; the chat client sends its own. Either way the report reaches the
application over the loopback interface.

Status therefore reflects what the agent is actually doing rather than what
its output happens to look like. Those settings are additive: your own
settings, hooks and permissions still apply.

One answer is not reported by any event: a permission prompt answered from
the keyboard. Enter turns the pane green, since allowing the tool starts it
running. Refusing it puts Claude Code back at its prompt without a word, so
a pane that is then heard from by nothing, neither an event nor anything it
draws, for ten seconds goes back to grey.

## An agent that cannot report

Not every coding agent has a lifecycle to report, and Flockdeck runs those too. For
their panes the status is read from the terminal instead: the bell an agent
rings when it wants you, a quiet timer for when it has stopped producing
output, and — where its entry in `agents.json` gives it `patterns` — the lines
it prints: the shape of a permission question, the shape of a prompt waiting
to be typed at. Only the last few hundred bytes are looked at, with the
escape sequences stripped, so a question two screens back does not
keep a finished pane amber.

That is a guess where the other is a fact, and it is worth knowing which you
are looking at: [Agents and models](#agents) says which agents report and which are
read. Where both exist, a reported event always wins.
