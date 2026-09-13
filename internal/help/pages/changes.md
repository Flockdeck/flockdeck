# Review, commit and push

[[key:changes]], or **Changes** in the rail, shows what changed in the working
tree the focused agent has been using. The **Review** button on each worktree opens the same panel for
that checkout, which is usually how you get here: see which agent produced
something, then look at what it did.

The panel always shows the whole repository the pane is in, even when the pane
was started in a folder inside it, and a commit takes every file in its list.

## What it shows

- The branch and where it stands against its upstream.
- Every changed file, what happened to it, and how many lines moved.
- A coloured diff of whichever file you select. A diff longer than 3,000 lines
  draws the first 3,000, with **Show them** for the rest, and the list of files
  stops at 2,000.

## What you can do

- **Commit**, with the message you type in the box.
- **Commit and push** in one go.
- **Fetch** and **Push** against the upstream.
- **Pull**, which appears only when the upstream has commits this branch does
  not, and only ever fast-forwards: a branch that has gone its own way is left
  for you to merge or rebase in a shell.

Everything that talks to a remote is there only when the checkout has one. The
first push sets the upstream, so a branch a fan-out invented does not need a
hand-typed command to leave the machine.

## What a commit takes

A commit takes the files the list showed you when the panel opened, or when
you last pressed **Refresh**. The list keeps up with the agents by itself, and
a file that joins it after you looked, or is written to again, is marked
**new** or **edited** rather than quietly added to what you commit.

If the working tree has moved since you looked — a file has appeared, one has
gone back to how it was committed, or one in the list has been written to
again — nothing is committed. The list then shows the tree as it is, with what
moved marked; check it and commit again. Only the files that were checked are
staged, so one written a moment after the check waits for the next commit.

During a merge, a file still in conflict is refused: a text file until its
`<<<<<<<` markers are gone, and a binary file, or one that only one side kept,
until you have chosen a side in a shell and added it.

What this does not promise:

- A file written to again is noticed by its size and modification time, not by
  reading it. An edit that leaves both as they were — possible where the file
  system keeps times only to the second — goes through.
- When the list stops at 2,000 files, the ones it leaves out are held to their
  number, not their names.
- A listed file written to in the instant between that check and the commit
  itself goes in as it is then.

Nothing here stages files selectively: a commit takes every file in the list,
whole. Reviewing the diff first is the point of the panel — for anything
finer, a shell pane is a keystroke away.
