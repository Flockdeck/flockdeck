# Fan out

Ask an agent to plan something and it answers with a list. Fan out reads that
list out of the pane's output and offers to start an agent for each item.

Press [[key:fanout]], or use the `⑂` button in the pane header.

## What the dialog does

The extracted tasks appear in an editable box, one per line. **Nothing runs
until you say so** — the list is a suggestion, not a decision. Edit it, delete
the items you did not mean, add ones the agent missed.

Three choices go with it:

- **Give each agent its own git worktree.** On by default in a repository.
  Each child gets a branch named after its task, so they work in parallel
  without touching each other's files. Their tabs are named after the task.
- **Split into this tab instead of new tabs**, when you want to watch them side
  by side rather than tab by tab.
- **Trust the new worktrees.** A fresh worktree is a directory Claude Code has
  never seen, so it would stop and ask whether the folder is trusted before
  doing any work — once per child. If the project you are fanning out from is
  already trusted, this carries that same answer over. It will not invent
  trust: the box is disabled unless the source directory is genuinely trusted
  already.

Each task is handed to Claude as its opening argument rather than typed into
the terminal, so it is submitted the moment the agent starts rather than
depending on guessing when the interface is ready.

Every child is a normal pane. Watch it, type into it, and review and commit
its work from **Changes**.

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
perch spawn "add tests for the parser"
perch spawn --worktree fix-auth "repair the token refresh"
perch spawn --split "watch the build"
```

Ask a lead agent to plan and then run one of these per task, and it fans
itself out. Only processes running inside a pane can do this: the token never
leaves the environment the pane was started with.
