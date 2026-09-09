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

This is not screen scraping. Each agent pane is launched with a generated
`--settings` file registering Claude Code's lifecycle hooks —
`UserPromptSubmit`, `PreToolUse`, `Notification`, `Stop` and the rest. Those
hooks re-invoke this same binary in a hidden mode, which reports the event to
the application over the loopback interface.

Status therefore reflects what the agent is actually doing rather than what
its output happens to look like. Those settings are additive: your own
settings, hooks and permissions still apply.
