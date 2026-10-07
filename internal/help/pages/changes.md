# Review, commit and push

[[key:changes]], or **Changes** in the rail, shows what changed in the working
tree the focused agent has been using. Clicking the change counts in a pane's
header opens it on that pane's checkout, and **Review changes**, in a
worktree's `⋯` menu, on that worktree's, which is usually how you get here:
see which agent produced something, then look at what it did.

The panel always shows the whole repository the pane is in, even when the pane
was started in a folder inside it, and a commit takes every file in its list.
In a project [grouping more than one directory](#projects), a picker at the
top chooses which repo the panel reviews. [Git worktrees](#worktrees) has
the same choice for creating a worktree.

## What it shows

- The branch and where it stands against its upstream.
- Every changed file, what happened to it, and how many lines moved.
- A coloured diff of whichever file you select. A diff longer than 3,000 lines
  draws the first 3,000, with **Show them** for the rest. The list of files
  stops at 2,000; a line under it counts the rest, which a commit still takes.

## What you can do

- **Commit**, with the message you type in the box. The button counts the
  files, as in **Commit 3 files**, and <kbd>Ctrl</kbd>+<kbd>Enter</kbd> in the
  box does the same. A message is required.
- **Commit and push** in one go.
- **Fetch** and **Push** against the upstream; Push counts the commits it
  would send, as in **Push 2**.
- **Pull**, which appears only when the upstream has commits this branch does
  not, and only ever fast-forwards: a branch that has gone its own way is left
  for you to merge or rebase in a shell, and is offered no Pull.
- **Refresh** reads the working tree again.

Everything that talks to a remote is there only when the checkout has one. The
first push sets the upstream, so a branch a fan-out invented does not need a
hand-typed command to leave the machine. A checkout with no branch checked out
(a detached HEAD, or one in the middle of a rebase or a bisect) says so in
place of the branch, and offers no push.

## What a commit takes

A commit takes the files the list showed you when the panel opened, or when
you last pressed **Refresh**. The list keeps up with the agents by itself, and
a file that joins it after you looked, or is written to again, is marked
**new** or **edited** rather than quietly added to what you commit.

If the working tree has moved since you looked (a file has appeared, one has
gone back to how it was committed, or one in the list has been written to
again), nothing is committed. The list then shows the tree as it is, with what
moved marked; check it and commit again. Only the files that were checked are
staged, so one written a moment after the check waits for the next commit.

During a merge, a file still in conflict is refused: a text file until its
`<<<<<<<` markers are gone, and a binary file, or one that only one side kept,
until you have chosen a side in a shell and added it.

What this does not promise:

- A file written to again is noticed by its size and modification time, not by
  reading it. An edit that leaves both as they were (possible where the file
  system keeps times only to the second) goes through.
- When the list stops at 2,000 files, the ones it leaves out are held to their
  number, not their names.
- A listed file written to in the instant between that check and the commit
  itself goes in as it is then.

Nothing here stages files selectively: a commit takes every file in the list,
whole. Reviewing the diff first is the point of the panel. For anything
finer, a shell pane is a keystroke away.

## When git is set up to run a program

Commit, Push, Pull and Fetch run git on your machine, as you, and git runs
whatever the repository tells it to: hooks in `.git/hooks`, a `core.hooksPath`,
a `core.sshCommand`, a credential helper, a filter driver, a signing program.
Many projects use hooks to lint or test before a commit, and tools such as husky
set `core.hooksPath` for you, so Flockdeck does not turn any of it off.

An agent that can write inside `.git` could add a hook, though, and the next
press of Commit would run it with no agent permission prompt in between. So
before it runs git for you, Flockdeck reads the repository's git configuration
(files it includes, a linked worktree's own configuration and each submodule's
too) and the hooks git would find. If something runs that was not there when you
last accepted, it stops and lists each program and the file it comes from. You
can cancel, or continue and then choose between remembering it for that
repository and accepting it just this time.

- A repository Flockdeck has not seen, where the repository itself names a
  program, asks once. A husky setup counts: Flockdeck cannot tell your tools
  from something an agent wrote.
- A repository where nothing is set to run is recorded without asking, so
  anything added later is a change.
- Your own system and global git configuration, such as a credential helper or
  Git LFS, is not asked about the first time. A change to it is.
- An edited hook is a change. Something removed is not.
- A hooks directory inside the working tree, such as `.husky`, is reported by its
  `core.hooksPath` setting alone. Its files are ordinary project files, and the
  list of changes shows them.

What you accepted is kept in Flockdeck's own state folder, not in the
repository. If the check cannot be made (git does not answer, say), the command
runs as it always did and a notice says the check was skipped.

The check runs before the buttons above. It does not see git commands you or an
agent run in a pane; an agent's own are for [auto-review](#status) and the
agent's permissions.
