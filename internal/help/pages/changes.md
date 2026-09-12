# Review, commit and push

[[key:changes]], or **Changes** in the rail, shows what changed in the working
tree the focused agent has been using. The **Review** button on each worktree opens the same panel for
that checkout, which is usually how you get here: see which agent produced
something, then look at what it did.

The panel always shows the whole repository the pane is in, even when the pane
was started in a folder inside it, and a commit takes all of it.

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

Nothing here stages files selectively: a commit takes the working tree as it
stands. Reviewing the diff first is the point of the panel — for anything
finer, a shell pane is a keystroke away.
