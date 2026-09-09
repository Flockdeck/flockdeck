# Review, commit and push

[[key:changes]] shows what changed in the working tree the focused agent has
been using. The **Review** button on each worktree opens the same panel for
that checkout, which is usually how you get here: see which agent produced
something, then look at what it did.

## What it shows

- The branch and where it stands against its upstream.
- Every changed file, what happened to it, and how many lines moved.
- A coloured diff of whichever file you select.

## What you can do

- **Commit**, with the message you type in the box.
- **Commit and push** in one go.
- **Push**, **Pull** and **Fetch** against the upstream.

The first push sets the upstream, so a branch a fan-out invented does not need
a hand-typed command to leave the machine.

Nothing here stages files selectively: a commit takes the working tree as it
stands. Reviewing the diff first is the point of the panel — for anything
finer, a shell pane is a keystroke away.
