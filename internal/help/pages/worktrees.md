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
the **Base** it starts from (filled in with the branch the repository has
checked out, which you can change), and
press **Create**, or <kbd>Enter</kbd> in either field. It is created in a
folder beside the repository's own, named after the repository and the
branch; giving an existing branch name checks that branch out instead of
creating one. **Branches without a worktree** are listed above it as one-click
buttons, up to fourteen of them; a branch further down that list is checked
out by typing its name into the form. In a project [grouping more than one
directory](#projects), the form also offers a choice of which repo to create
the worktree in (defaulting to whichever is active), since a worktree always
belongs to exactly one repository; [Review, commit and push](#changes) has the
same choice for reviewing one.

**Prune gone**, at the top of the list and shown only while some worktree's
folder is gone, drops git's records of worktrees whose folders were deleted
outside the application. A worktree like that is listed
as **folder gone**, with a **Prune** button of its own in place of the others,
since there is no folder left to open an agent, a shell or a review in. Either
button clears every such record at once, including one for a worktree on a
drive that is not plugged in.

## In the pane headers

Every pane header carries the same information for the checkout it is working
in (branch, `●n` uncommitted files, `↑n` and `↓n` against upstream),
refreshed in the background while its project is on screen. That is the state
of every agent's tree at a glance, without opening anything, and clicking the
counts opens [Changes](#changes) on that pane's checkout. The panes of the
other open projects are read when you switch to one, and when the Agents
overview opens.

Work left uncommitted inside a submodule is not counted there: finding it
means running git inside every submodule on every refresh. The review panel
still shows it, and a submodule moved to another commit is counted in both.

Each checkout is read on its own. One that git does not answer for within ten
seconds (a very large checkout, or one on a network drive gone quiet) holds
up no other pane, and its own headers say *git timed out* in place of counts
that may be out of date. It is asked again on the next refresh; running
`git status` in a terminal there shows what is slow.

## Conflicts between panes

With the **Conflict radar** turned on (**General › Conflict radar** in the
settings, off until you turn it on), a pane header grows a `⚠` chip with the
name of another pane when git predicts that the two panes' work would conflict
if both were merged. Hover the chip for a summary. Press it, or tab to it and
press Enter, for a list of each pane, its branch and the files git stops on
(up to five panes and twenty files each); Escape, or a press elsewhere, puts it
away. With more than one other pane the chip names the first and counts the rest. The chip is drawn in the plain text colour: the
green, amber, grey and red of a pane stay for what its agent is doing.

How it works:

- It runs with the git refresh, about every 15 seconds and sooner after a
  commit or a pane opening, for the panes on screen. It compares only panes in
  the same repository, and only a pair that has changed some of the same files,
  or a file and a directory of the same name.
- A pane is skipped when it is clean and has nothing against its base (see
  below), detached, in the middle of a rebase,
  merge, cherry-pick, revert, am or bisect, with unmerged files, before its
  first commit, or with more than a thousand changed files. A skipped pane never
  shows a chip. A clean pane on a commit it was already found to have nothing
  against costs no git process.
- In a repository with two or more panes, every pane that is not skipped is
  copied, whether or not it shares a file with another, since that is what finds
  out: what the checkout holds, committed or not, untracked files included, goes
  into a scratch folder in the system's temporary folder, which is deleted when
  the refresh is done. A folder left by a run that was killed is removed the
  first time the radar runs after the next start, if it is over an hour old. Only
  panes that share a file, a file and a directory of one name, or a directory a
  file was moved out of and a file added to it, are then compared. git merges the copies of two panes
  in memory (`git merge-tree`), so the answer is the one a real merge would
  give: edits to lines far apart are fine, edits to neighbouring lines are a
  conflict, a file deleted on one side and edited on the other is a conflict, and
  one pane renaming a file the other edits is not.
- The radar's own copies never write to a checkout: not its index, its lock, its
  files or its branches. git may set the modification time of an object file in
  the repository that it finds it would have written, and, under a split index
  in a linked worktree, of the shared index file there. A clean filter such as
  Git LFS, which git runs on the copies, may write its own files there; that has
  not been checked. A merge driver that a `.gitattributes` names is run by the
  merge, and has not been checked either.
- The refresh of a checkout's index is a separate job, run with the git refresh
  whether or not the radar is on. It rewrites the index as git does when it
  records what it found about files that were touched without changing. It never
  removes an index.lock and never kills a git it started: a lock that is there is
  left and that refresh is skipped, and the log says so once an hour for a lock
  more than an hour old. A refresh still running when Flockdeck quits is left to
  finish by itself. One that has run for an hour (a stuck network drive) no
  longer keeps the checkout from being refreshed: one more attempt is made, if no
  lock is there, and never more than two are running at once. An hour is also
  how long between the log's reminders that one is still running.
- The chip shows once the same pair has been found conflicting on two refreshes
  in a row, at least five seconds apart, so an edit caught half way does not
  flash it. It goes when a later refresh finds the pair no longer conflicting,
  or one of the two panes has nothing left to compare. A refresh that could not
  read a pane (git failed, or ran out of time) changes nothing about the pairs
  that pane is in.
- Pane names, branches and file paths go to every open window, including one
  reached through the relay, as the rest of a pane's header does. File contents
  are never sent.

No chip is not a promise. The pane may have been skipped for one of the reasons
above, or the pair may not have conflicted on two refreshes yet, or its project
may not be on screen (only the panes of the project on screen are refreshed, so
a chip about the others is as old as the last time they were), or a check may
have failed or timed out. A chip is a prediction for one pair of panes, made
from the last time both were read. Neither says anything about whether the
merged code builds or passes its tests: a rename whose imports are used
elsewhere merges cleanly and still breaks.

What it costs: a pane with uncommitted or new files is copied and compared on
every refresh. Copying a pane takes eleven git processes when it has uncommitted or new files,
four when it has only commits, and fourteen when its index cannot be copied and
is rebuilt (a sparse checkout is not copied then, and costs four before it is
left out); choosing the base again, or checking a shallow clone, adds a few. On
top of that come the status of each checkout, which the refresh runs anyway, one
process per repository that asks whether the bases moved, and a merge for each
pair of panes that share a path. Every untracked file is read again each time.
A checkout whose git commands keep running past 20 seconds, or whose copy was
itself running for at least 15 seconds when the radar's 30 seconds ran out
(usually for a very large untracked file such as a video; a checkout queued
behind others, or that had only just begun, is not held to account), is skipped
after three such refreshes in a row. A quiet checkout that is always slow is
skipped for 10, 20, 40, 80 and then at most 120 minutes. One whose commit or
counts of changed and new files keep changing is tried again at every refresh and
pays up to the radar's 30 seconds each time: the skip ends with the change, but
its strikes are kept and raise the step, which matters only while it stays quiet.
Strikes are forgotten after a snapshot that works, when the radar is turned off
and on, and after a day without one. The log says which it was and what was
running; nothing in the window shows a skipped checkout. That cannot be saved by keeping what was read: the copies
of the files go into a scratch folder that is deleted with each refresh, and an
index that remembers them points at objects that folder held, so keeping one
means keeping the other, which grows without a limit that is safe to prune while
a refresh may be reading it. The cost is bounded by the limit of a thousand
changed files. Each pair that shares a file adds a merge, and the same two copies
are not merged twice. On a very large checkout this is noticeable, which is
why it is off by default.

The base. Work is measured from the nearest of these branches that shares
history with the pane, meaning the one with the fewest commits between where
they meet and the pane's HEAD: the branch the main worktree has checked out
(unless it is on a detached HEAD), the branch a bare repository's HEAD names,
`origin`'s default branch, and the branches called `main` and `master`, looked
up as branches and not tags. Where two are equally near the one listed first
wins. A branch with no history in common with the pane is never chosen, so a
stale `origin` default, or an unrelated `main`, is passed over for a better
one. Every branch that resolved is watched, including one at the same commit as
another, and so is the main worktree's HEAD, and the candidates are looked up
again when any of them moved, which is checked at every refresh. So a commit to
the base branch, a fetch that moves `origin`'s default, or a main worktree that
gets a branch of its own, is noticed at the next refresh. A bare repository's
HEAD pointed at a branch with another commit is noticed too. What the check cannot
see is a branch that did not exist when the candidates were looked up and is
created later (a new `main` or `master`), or a bare HEAD pointed at a branch at
the same commit; those wait for the next lookup, which happens when a watched ref
moves, when the 5 second memory of finding no base runs out, or at the latest ten
minutes after the last.

Limits, plainly:

- A pane that shares history with none of those branches is not checked, and
  what was shown about its pairs is kept. A shallow clone whose history does not
  reach the base is not checked either, since it cannot say whether the two are
  related. When no branch is found at all the panes are not checked, and the
  search is made at most once every 5 seconds, and only on a refresh, which is
  about every 15. A repository whose real base is none of these is measured from
  the wrong one, or not at all. `origin`'s default is as it was last recorded,
  and is only passed over, never refreshed.
- A partial clone fetches the objects it lacks from its remote when git needs
  them. From git 2.44 the radar tells git not to. On 2.38 to 2.43 it cannot, so
  it is not run in a partial clone there, and says so in the log.
- A submodule moved to another commit is a changed path, so two panes moving it
  differently meet, and `merge-tree` decides. Checked: where the submodule is
  not checked out in the panes' folders, `merge-tree` calls any two different
  moves a conflict, even where one commit descends from the other and they
  could be combined; where it is checked out in both, such a pair gives no
  conflict, and so no chip. Two moves that cannot be combined are a conflict.
- A folder holding a repository of its own that nobody added, with commits or
  with none (a fresh `git init`), is not a changed path, so it cannot make a chip,
  and does not stop the checkout from being compared. One that was added with `git add`, or
  registered as a submodule, is. A pane whose only change is such a folder is
  copied again at every refresh, since its status cannot be called clean.
- A pane whose folder was deleted and which git cannot read for three refreshes
  in a row stops holding the chip on the pane it conflicted with. Before that,
  the chip stays.
- The directories a file moved out of are kept as a set of their own, so that a
  file another pane adds to one of them is found, and it too is limited to a
  thousand. Only changed files count toward the limit of a thousand changed
  paths.
- A pane with more than a thousand files against the base, and nothing
  uncommitted, is remembered as skipped until its commit or the base moves.
- Two commits are told apart by their whole id.
- On Windows, git does not list a file whose path is longer than 260 characters
  unless `core.longpaths` is on. The copies the radar makes have it on, but the
  status that decides whether a pane is clean does not, so a pane whose only
  change is such a file looks clean and is not compared. Setting `core.longpaths`
  in the repository fixes that. A second status of every clean pane on each
  refresh would find it, and costs more than the case is worth.

It needs git 2.38 or newer. On an older git the switch is dimmed and says so.
In a sparse checkout whose index cannot be copied, the pane is not checked until
it can be.
