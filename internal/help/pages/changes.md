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
- A hooks directory inside the working tree, such as `.husky/_`, is read like
  any other, so an edited hook there is a change. A Pull can bring one, and
  nothing in the list of changes shows it at that point. Scripts a hook goes on
  to call from elsewhere are not read.
- A remote that is a folder on this machine is listed along with the hooks that
  folder's repository would run when you push to it, including the ones in its
  own `core.hooksPath`. That covers a remote named by a branch's `remote` or
  `pushRemote` setting or by `remote.pushDefault`, a `file://` address (with
  `%XX` spelling and `localhost` read the way git reads them), a remote that is a
  linked worktree, and an address that a `url.<base>.insteadOf` or
  `pushInsteadOf` rule turns into a folder. A remote that uses a helper such as
  `ext::`, which runs a command, is listed too. A folder that is there and
  cannot be told from a repository, a `file://` address for another machine and
  a network share are listed as things Flockdeck could not read. Flockdeck does
  not open a network share, since Windows would connect to it.
- Merge drivers, which `.gitattributes` can assign to any file, are listed with
  the other settings that run a program. On Windows a hook named `pre-commit.exe`
  counts as the `pre-commit` hook, as it does for git.
- Something git may run but Flockdeck could not read, such as a hooks folder with
  no permissions, is listed as that. So is a search that stopped at its limit
  (200 submodules, 2,000 folders under `.git/modules`, or more than six levels
  of them) and a configuration file over 4 MB. Once you accept the submodule or
  folder limit, a repository that grows further past it is not asked about
  again. A configuration file over 4 MB is asked about again when its size
  changes.
- A scan that cannot be finished is listed too, and is asked about every time: it
  is not remembered. That covers git not answering within 15 seconds, git
  printing more than 8 MB for one question, a hook too large to read within the
  64 MB a scan reads in all, and a path such as `~user/hooks` that Flockdeck
  cannot work out. A repository can be made slow to read on purpose, so slow is
  not treated as safe.
- A hook, setting or path is shown cut to a length, with line breaks and other
  control characters removed.

What you accepted is kept in Flockdeck's own state folder, not in the
repository, and it only grows: what you accepted for a repository stays
accepted, so linked worktrees that differ a little do not undo each other. The
file is one an agent running as you could write too, so this protects you from
an agent that can write inside the project and not from one that can write
anywhere you can. Two Flockdeck windows of one user may overwrite each other's
record at the same moment; the worst result is being asked again.

Only a folder that is not a git repository, or a machine with no git, goes
ahead without the check, since git has nothing to run there. A record that cannot
be read counts as no record, so the repository's own programs are asked about.

The check is not atomic. A commit is looked at when you press the button and
again once your files are staged, just before git commit starts, and it is
refused if what runs has changed. Staging is `git add`, which runs clean filters
and the `post-index-change` hook before that second look, so those two are
covered only by the first. The second look narrows the gap to the moments
between it and git reading the hooks and settings; it does not close it, and a
determined agent that can write inside `.git` can still race it. Pull, Push and
Fetch are looked at once, just before. A window reached through the relay is
shown the reason and told to press the button on the machine itself; approving
is done there.

This covers the four buttons above. It is not every program a repository can
make Flockdeck run. Still outside it:

- Git commands Flockdeck runs in the background to draw status, diffs and the
  radar (including `git add` on a scratch index and the index refresh) run a
  repository's clean filters on a file whose timestamp has changed. Filters cannot
  be switched off for all names at once. These commands do run with
  `core.hooksPath` turned off, so their hooks (`post-index-change`,
  `reference-transaction`) do not run, and with `core.fsmonitor` off.
- Creating a worktree runs `post-checkout` and the smudge filters of the
  repository, as git does.
- Hooks of anything you or an agent run in a pane, and scripts a hook goes on to
  call.
- A remote reached as `ssh://localhost/...` or `host:path` that happens to be
  this machine: Flockdeck cannot tell, so the hooks on the other end are not
  listed.
- An agent running as you that can write the file of accepted state.

An agent's own git commands are for [auto-review](#status) and the agent's
permissions. Flockdeck never runs `git mergetool`, so `merge.tool` and
`mergetool.*` are not read, and `merge.<name>.recursive` only names another
driver.
