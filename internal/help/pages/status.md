# Knowing who needs you

Running one agent is easy. Running six is not: they finish at different times
and they block on permission prompts, and the one that is waiting is rarely
the one you are looking at.

## The dots

Every pane header carries a status dot.

| Dot | Meaning |
| --- | --- |
| Filled cyan circle, pulsing | **Working** — producing output or running a tool |
| Amber triangle | **Waiting on you** — a permission prompt or a question |
| Amber triangle | **Blocked** — a tool call was refused outright and the turn ended there; nothing is being asked, but it needs a look |
| Hollow grey circle | **Idle** — it finished its turn and is ready for a new prompt |
| Hollow grey circle, pulsing | **Starting** — launched, and not heard from yet |
| Red filled square | **Failed** — the process ended with an error, or was killed |
| Hollow square | **Exited** — the process is gone |

Each state has its own shape as well as its colour, so you can tell them apart
without seeing colour, except that waiting and blocked share the triangle. A
blocked pane counts as waiting everywhere waiting agents are counted (below).

A pane whose turn is over but that has background work still running, a
command or subagent it started, is still **Idle** and its header says how many
are "in background". Its dot is the working dot all the same, and its tooltip
says it is working in the background. Such an agent is counted as working
everywhere working agents are counted (below), though its own status stays
idle.

While a tool is running the pane header names it, so `Read`, `Bash` or `Edit`
tells you what the agent is actually doing rather than only that it is busy.

## Where else it shows

- **The tab** holding a waiting agent is marked `▲`. A tab with an agent
  working, or idle between turns with background work still running, has a
  pulsing filled dot instead; the `▲` takes its place when an agent there is
  waiting.
- **The window title** reports the count, so a waiting agent is visible in the
  taskbar with the window behind something else.
- **The top bar** counts the agents waiting and working across every open
  project, the same count the window title carries; an
  idle agent whose background work is still running counts as working, here
  and in the window title and the tab icon. Click
  the count for the same list [[key:agents]] opens.
- **The rail** down the left of the window has a tile for each open project,
  and the tile says what that project's agents are doing, most urgent first: an
  amber badge where an agent is waiting on you, a pulsing cyan one where
  agents are working (one with background work still running counts) and
  none is waiting, and nothing where every pane is idle, exited or not yet
  heard from, or there are no panes. So one that
  starts waiting in a project you are *not* looking at is seen from the one
  you are. Widen the rail and the count is written beside the name — `▲ 2`
  for two waiting, `● 3` for three working — and the tile's tooltip and
  screen-reader name say it in words. A window reached through the relay
  shows the same.
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
- [[action:closeFinishedPanes]] clears out every pane that has gone idle or
  exited, in every open project, in one go — the tidying-up equivalent of
  closing finished browser tabs. It asks nothing first, on purpose: a pane
  still waiting on you or still working is never touched, and one whose last
  turn failed is left alone too, since a failure is worth a look before it
  disappears. A pane the user has locked is left open too, with a count of how
  many were skipped. An idle agent that still has background work going — a command
  it started with `run_in_background`, or a background subagent — is not
  finished either, since closing it would kill that work. Its header says
  `◔ 1 in background` while it has any, and so does its row in
  [[key:agents]]. Flockdeck learns the work has ended when Claude Code tells
  the agent so, or says at the end of a turn what is still running. A Claude
  Code too old to say either keeps it counted until the agent stops it, the
  conversation is cleared, or the pane is closed by hand (or with
  `flockdeck close --force`).
- A background subagent calling tools after its agent's turn has ended leaves
  the pane idle, with that count and the steady working dot, rather than
  flashing cyan for each call.
  A question or permission prompt it puts to you still turns it amber.

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

### Auto-review approvals

A pane can turn on auto-review approvals, which spares a person the wait for
some of what would otherwise stop and ask: the same hidden hook that reports
a `PreToolUse` call can also carry a permission decision back, before Claude
Code's own prompt ever opens, for a call auto-review is confident about. It
works like Codex's `auto_review` -- a reviewer looking at the request in
front of it -- but it never touches Claude Code's own permission settings and
it can only ever say "let this one through," never "deny": off, or for a call
it is not sure of, a pane behaves exactly as it always would. Today that
reviewer is a fixed policy: a `Bash` call is let through only when it is a
single call to a command that only ever reads -- `git status`, `cat`, `grep`
and the like -- inside the project, with nothing in it that could chain into
something else or write anywhere; a call that changes a file, reads outside
the project, or reads a secret file (a `.env`, a private key, a credentials
file) is always left to ask. A `git` command is let through only when git
itself, asked where the command will run, has nothing configured that would
run a program or reach the network, and `git status` or `git diff` never in a
repository with a submodule in it.

These path checks read the command as text, not the filesystem -- auto-review
is not a sandbox. A symlink inside the project that points outside it reads as
an ordinary in-project name and is let through, and the secret-file check
knows only conventional names. Both err towards reading, which is part of why
auto-review is off unless you turn it on, and why you should turn it on only
for a pane whose agent you trust.

It is off for a pane unless you turn it on, with the ✓ in the pane's header,
or for new panes with **Settings › Behaviour › Start new panes with
auto-review on**; a pane started by another agent starts as that agent's
pane is. A pane keeps its setting
across restarts, whichever way you left it.

One answer is not reported by any event: a permission prompt answered from
the keyboard. Enter turns the pane cyan, since allowing the tool starts it
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

### Letting Jev break the tie

The one thing the terminal cannot tell is a pane that has gone quiet: finished,
or stopped on a question. Flockdeck can ask TypeSafe's Jev model, a small
model that answers a fixed multiple-choice question rather than writing
anything. **This sends terminal output to a third party, TypeSafe**, which
nothing else in Flockdeck does with what your panes print, so it is **off by
default** and needs both of these:

1. **Settings › Behaviour › Status detection** turned on. It can only be turned
   on from the machine itself, not from a window reached through the relay;
   turning it off works from anywhere.
2. A TypeSafe API key: either the one you paste into **Settings › Behaviour ›
   TypeSafe API key** (kept on this machine with your other keys, never shown
   back, only ever sent to `api.typesafe.ai`, and only set from the machine
   itself), or `TYPESAFE_API_KEY` in the environment Flockdeck starts in. The
   one in Settings is used first. Saving a key does not turn anything on.

With either missing, nothing is ever sent, and no error is shown.

What is sent, when a pane of an agent that reports nothing has been quiet for
a few seconds and its status is still a guess: the **last 30 lines of its
output, at most 2,000 characters, with the escape sequences taken out** — one
request holding that text and the two questions asked of it. Not the
scrollback, not the pane's name or folder. Secrets that happen to be on screen
in those lines are **not** removed. Shell panes, and agents that report their
own status, are never sent.

What it can change: a quiet pane called idle becomes waiting, or blocked, only
when Jev answers with high confidence. It never turns waiting or blocked into
anything else, and if it errors, is slow, is rate limited, or is unsure, the
pane shows what it would have shown without it. Asks are limited to one per
pane every 15 seconds, twelve per pane an hour and ten a minute in all, and
the same output is never asked about twice.
