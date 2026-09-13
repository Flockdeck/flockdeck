# Fan out

Ask an agent to plan something and it answers with a list. Fan out reads that
list out of the pane's output and offers to start an agent for each item.

Press [[key:fanout]], or use the `⑂` button in the pane header.

For a Claude Code pane the list is read out of the agent's own transcript — the
markdown it actually produced, rather than the wrapped and redrawn version of
it on screen. Any other pane, the built-in chat client and shells among them,
falls back to the screen and is more often wrong for it.

## What the dialog does

The extracted tasks appear in an editable box, one per line. **Nothing runs
until you say so** — the list is a suggestion, not a decision. Edit it, delete
the items you did not mean, add ones the agent missed. <kbd>Enter</kbd> in the
box starts a new line rather than the run: the **Start** button under it, which
counts the agents, is what starts them, as does <kbd>Ctrl</kbd>+<kbd>Enter</kbd>
in the box, and <kbd>Esc</kbd> closes the dialog without starting any.

These choices go with it:

- **Which agent, and which model.** One control at the top sets what the whole
  run uses, and starts on the project's default agent and model; each line
  carries an override of its own, so twelve tasks can be split between two
  agents on purpose — the one that is good at the refactor and the cheap one
  that is good enough for the six renames.
- **Routing**, when it is on for the project. A line whose task one of the
  rules matches comes with its model already set — a smaller one for
  mechanical work, a stronger one for hard work — and tagged **↘ routed** or
  **↗ routed**; the tag's tooltip names the rule. One line above the tasks
  counts them, beside **Use the run's model for every task**, which puts every
  line back on the run's model. Change a line's model and it is yours again.
  What the dialog shows is what runs. [Agents and models](#agents) has the
  rules, and how to turn routing on.
- **Give each agent its own git worktree.** On by default in a repository.
  Each child gets a branch named after its task, so they work in parallel
  without touching each other's files. [Git worktrees](#worktrees) covers what
  you can do with them afterwards.
- **Put them in this tab, beside the agent that planned them.** Off, the
  children get a new tab of their own, called **Fan out** — or named after the
  task, when there is only one. Either way they end
  up in one tab together rather than a tab each: a dozen agents is a dozen tabs
  nobody can read, and a fan-out is precisely when you want to see them at once.
- **Trust the new worktrees.** A fresh worktree is a directory the agent has
  never seen, so an agent with a trust question of its own — Claude Code has
  one — would stop and ask whether the folder is trusted before doing any work,
  once per child. If the project you are fanning out from is already trusted,
  this carries that same answer over. It will not invent trust: the box is
  disabled unless the source directory is genuinely trusted already. The
  project's answer to Claude Code's second question, "Allow external CLAUDE.md
  file imports?", comes across the same way: a yes stays a yes, a no stays a
  no, and nothing is written if the project was never asked. The questions are
  Claude Code's, so it does nothing for another agent.

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

## How many at once

A fan-out will start at most **12** agents. Each one is a real agent in a real
terminal, and past a dozen it is the machine rather than the plan that decides
how well they run. If the list you have edited is longer, the first 12 are
started and it says so; start the rest as a second fan-out once some of the
first have finished.

Blank lines in the box are not tasks and do not count towards it.

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
