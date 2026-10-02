# Git worktrees

Worktrees are how you run several agents without them fighting over one
checkout: each gets its own directory and its own branch, backed by the same
repository. [[key:worktrees]], or **Worktrees** in the rail, opens the panel.

## What the panel shows

For every worktree:

- the branch, or `detached@abc1234`, and the short commit
- **how many agents are already working in it**
- uncommitted work, split into changed and new files
- how far ahead or behind its upstream it is
- whether it is the main worktree, or locked
- **folder gone**, when its folder was deleted outside the application and
  only git's record of it is left
- in a project [grouping more than one directory](#projects), which repo the
  worktree belongs to

## What you can do

Each row has one button, **Open agent**, which opens an agent tab in that
worktree. The rest are in the `⋯` menu beside it, which a right-click on the
row opens too:

| Action | What it does |
| --- | --- |
| **Open agent** | Open an agent tab in that worktree |
| **Open shell** | Open a plain shell there instead |
| **Split beside this pane** | Add an agent for it beside the pane you are looking at |
| **Review changes** | See what changed there, commit and push |
| **Remove worktree…** | Delete the worktree's folder. When it has uncommitted or new files you are asked first, since removing it discards them. One with any pane open in it is refused, with or without uncommitted work; close them first. The main worktree has no Remove |

The keyboard walks the list with the up and down arrows, and the left and
right arrows move between a row's two buttons. **Refresh**, at the top, reads
the list again.

**New worktree**, at the foot of the panel, creates one: type the branch, and
the **Base** it starts from — filled in with the branch the repository has
checked out, which you can change — and
press **Create**, or <kbd>Enter</kbd> in either field. It is created in a
folder beside the repository's own, named after the repository and the
branch; giving an existing branch name checks that branch out instead of
creating one. **Branches without a worktree** are listed above it as one-click
buttons, up to fourteen of them; a branch further down that list is checked
out by typing its name into the form. In a project [grouping more than one
directory](#projects), the form also offers a choice of which repo to create
the worktree in — defaulting to whichever is active — since a worktree always
belongs to exactly one repository; [Review, commit and push](#changes) has the
same choice for reviewing one.

**Prune gone**, at the top of the list, drops git's records of worktrees whose
folders were deleted outside the application. A worktree like that is listed
as **folder gone**, with a **Prune** button of its own in place of the others,
since there is no folder left to open an agent, a shell or a review in. Either
button clears every such record at once, including one for a worktree on a
drive that is not plugged in.

## In the pane headers

Every pane header carries the same information for the checkout it is working
in — branch, `●n` uncommitted files, `↑n` and `↓n` against upstream —
refreshed in the background while its project is on screen. That is the state
of every agent's tree at a glance, without opening anything, and clicking the
counts opens [Changes](#changes) on that pane's checkout. The panes of the
other open projects are read when you switch to one, and when the Agents
overview opens.

Work left uncommitted inside a submodule is not counted there: finding it
means running git inside every submodule on every refresh. The review panel
still shows it, and a submodule moved to another commit is counted in both.

Each checkout is read on its own. One that git does not answer for within ten
seconds — a very large checkout, or one on a network drive gone quiet — holds
up no other pane, and its own headers say *git timed out* in place of counts
that may be out of date. It is asked again on the next refresh; running
`git status` in a terminal there shows what is slow.
