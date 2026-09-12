# Git worktrees

Worktrees are how you run several agents without them fighting over one
checkout: each gets its own directory and its own branch, backed by the same
repository. [[key:worktrees]] opens the panel.

## What the panel shows

For every worktree:

- the branch, or `detached@abc1234`, and the short commit
- **how many agents are already working in it**
- uncommitted work, split into changed and new files
- how far ahead or behind its upstream it is
- whether it is the main worktree, or locked

## What you can do

| Action | What it does |
| --- | --- |
| **Agent** | Open an agent tab in that worktree |
| **Shell** | Open a plain shell there instead |
| **Split** | Add an agent for it beside the pane you are looking at |
| **Review** | See what changed there, commit and push |
| **Remove** | Delete the worktree — after asking, when it has uncommitted work that would be discarded. A clean one with panes still working in it is refused; close them first |

**New worktree** creates one for a new branch off any base ref. It is created
next to the repository; giving an existing branch name checks that branch out
instead of creating one. Branches that have no worktree are listed underneath
as one-click buttons.

**Prune** drops git's records of worktrees whose folders were deleted outside
the application.

## In the pane headers

Every pane header carries the same information for the checkout it is working
in — branch, `●n` uncommitted files, `↑n` and `↓n` against upstream —
refreshed in the background. That is the state of every agent's tree at a
glance, without opening anything.
