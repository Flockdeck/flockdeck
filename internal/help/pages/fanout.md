# Fan out

Ask an agent to plan something and it answers with a list. Fan out reads that
list out of the pane's output and offers to start an agent for each item.

Press [[key:fanout]], or use the `⑂` button in the pane header.

For a plan you want to work through over days rather than start all at once,
see [Todo](#todo) instead.

For a Claude Code pane, and for a model API spoken to through the built-in
chat client, the list is read out of the agent's own stored conversation — the
markdown it actually produced, rather than the wrapped and redrawn version of
it on screen — from its latest reply that holds one, so a plan followed by an
answer to a follow-up question is not lost. Any other pane, shells among them,
falls back to the screen and is more often wrong for it.

## What the dialog does

The plan is shown as a list of **Tasks**, one row each, every row its own
field. **Nothing runs until you say so** — the list is a suggestion, not a
decision. Edit a row in place, empty it to take the task out, and type a task
the agent missed into **Add a task…** below the last row. <kbd>Enter</kbd>
moves to the next row rather than starting anything. A list of several lines
pasted into a row becomes that many tasks; **Edit as text** swaps the rows for
one box with a task per line, for pasting or rewriting the whole list at once,
and pressing it again brings the rows back.

The footer counts what will start — how many agents, and on which models — and
**Start agents** (it reads **Start 3 agents** and so on) is what starts them,
as does <kbd>Ctrl</kbd>+<kbd>Enter</kbd> anywhere in the dialog. **Cancel**, or
<kbd>Esc</kbd>, closes it without starting any.

If the pane ends in a question the agent is asking you — a permission prompt —
the dialog says so rather than offering the prompt's choices as tasks: answer
it in the pane, then fan out again, or type the tasks yourself. A pane with no
plan in it opens on an empty list, ready to type into.

These choices go with it:

- **Model for every task.** One control at the top sets the agent and model
  the whole run uses, and starts on the project's default; each row has a
  control of its own, on **Same as the run** until you change it, so twelve
  tasks can be split between two agents on purpose — the one that is good at
  the refactor and the cheap one that is good enough for the six renames.
  These controls are shown only where there is more than one agent or model to
  choose from.
- **Routing**, when it is on for the project. A row whose task one of the
  rules matches comes with its model already set — a smaller one for
  mechanical work, a stronger one for hard work — and tagged **↘ routed** or
  **↗ routed**; the tag's tooltip gives the rule's reason. A line under the
  run's model sums it up, as in **Routing: 2 down, 1 up**, beside **Undo
  routing**, which puts every routed row back on the run's model. Change a
  row's model and it is yours again. What the dialog shows is what runs.
  [Agents and models](#agents) has the rules, and how to turn routing on.

Under **Where they run**:

- **Each in its own git worktree.** On by default in a repository. Each child
  gets a branch named after its task, so they work in parallel without
  touching each other's files. Fanned out from a folder inside the repository,
  each child works in that same folder of its worktree; a folder git does not
  track has no copy there, so the child starts at the top. Outside a
  repository the switch is off and says the agents share this directory.
  [Git worktrees](#worktrees) covers what you can do with them afterwards.
- **New tab** or **Beside the planner.** A new tab of their own is called
  **Fan out** — or is named after the task, when there is only one; beside
  the planner puts them in this tab, next to the agent that planned them.
  Either way they end up in one tab together rather than a tab each: a dozen
  agents is a dozen tabs nobody can read, and a fan-out is precisely when you
  want to see them at once. Which one the dialog opens on is set under
  **Settings › General › New panes**.
- **Trust the new worktrees.** A fresh worktree is a directory the agent has
  never seen, so an agent with a trust question of its own — Claude Code has
  one — would stop and ask whether the folder is trusted before doing any work,
  once per child. If the folder you are fanning out from is already trusted,
  this carries that same answer over to the folder each child works in. It will
  not invent trust: the box can be ticked only when the source directory is
  genuinely trusted already and the agents get worktrees, and an answer given
  for one folder of a repository goes to that same folder of each worktree,
  never to the whole worktree. The project's answer to Claude Code's second
  question, "Allow external CLAUDE.md file imports?", comes across the same
  way: a yes stays a yes, a no stays a no, and nothing is written if the
  project was never asked. The questions are Claude Code's, so the box is
  shown only when the run starts Claude Code.

Each task is handed to the agent as its opening argument rather than typed into
the terminal, so it is submitted the moment the agent starts rather than
depending on guessing when the interface is ready.

## How they are arranged

The children are laid out in rows of even columns — three panes are a row of
three, twelve are three rows of four — and the grid is rebuilt as each one
starts. A row of twelve wraps every line a terminal prints and a stack of
twelve leaves four lines showing, so neither is a tab you can actually watch.

As soon as the first child starts, the window goes to it: its tab is selected
and its pane focused, so what you type next reaches it. In this tab, the focus
moves off the agent that planned them onto the first new pane. If you have
gone to another tab while the worktrees were being made, the window stays
where you are, so nothing you are typing goes to an agent you did not pick.

Rearrange them afterwards like any other pane: drag one onto another's edge,
drag one onto the `+` in the tab bar for a tab of its own, or drag a divider.

Every child is a normal pane. Watch it, type into it, and review and commit
its work from [Changes](#changes).

## When it settles

Once nothing in it is left working or starting, a gathered tab collapses to a
summary: one line per agent, its branch, and how it stopped — done, needing
your input, or failed, each with the detail that goes with it. A line opens
its own pane the same way zooming into any other does; **Back to grid** takes
the whole tab back to its terminals. Dismissing it this way is not permanent:
a **Show summary** chip stays over the grid for as long as the tab stays
settled, and brings the card straight back. Starting the tab working again —
restarting a pane, say — clears the chip along with the dismissal.

Closing a settled job, tab and all or one pane at a time, does not throw its
outcome away. [[action:fanoutHistory]], in the rail and the command palette,
lists a project's past fan-out jobs for as long as Flockdeck runs — what was
fanned out, when, and each pane's own outcome, read the same way the card
itself reads them. It is kept in memory only: quitting Flockdeck clears it,
the same as everything else a fan-out is not asked to write to disk.

## How many at once

A fan-out will start at most **12** agents. Each one is a real agent in a real
terminal, and past a dozen it is the machine rather than the plan that decides
how well they run. The heading over the tasks counts them against the cap, as
in **5 of 12**. In a list longer than that the rows past the twelfth are struck
through, and the footer says only the first 12 start; run the rest
as a second fan-out once some of the first have finished.

Blank lines are not tasks and do not count towards it.

## When only some of them start

A task that cannot be started does not cancel the others. Each one that fails
is reported on its own, and the summary at the end says how many agents
started and how many did not — a fan-out opens a screenful of panes, and
without the count a task that never started reads as one you simply lost track
of among the ones that did.

A worktree cut for an agent that then failed to start is removed again, so the
worktree list is left describing the agents that are really running.

## An agent starting its own helpers

Every pane carries an address and a token in its environment, so an agent can
hand work to helpers itself:

```sh
flockdeck spawn "add tests for the parser"
flockdeck spawn --worktree fix-auth "repair the token refresh"
flockdeck spawn --split "watch the build"
```

Ask a lead agent to plan and then run one of these per task, and it fans
itself out. Only processes running inside a pane can do this: the token never
leaves the environment the pane was started with.

A helper started this way is its parent's responsibility to watch, not yours:
its finishing and going quiet never raises a phone push, a desktop
notification or counts toward the waiting badges, since Claude Code's own
idle nudge means nothing more than that the pane has gone quiet — true of
any pane, not only a helper's. A helper asking permission or a real question
still turns amber and reaches you as usual, since only you can answer those.
