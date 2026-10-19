# Auto-approve read-only

Earlier versions called this auto-review. It has nothing to do with the diff
review in [Changes](#changes), which is where you read what an agent changed.

A pane can turn on **Auto-approve read-only**, which spares you the wait for
some of what would otherwise stop and ask: the same hidden hook that reports
a `PreToolUse` call can also carry a permission decision back, before Claude
Code's own prompt ever opens, for a call Flockdeck is confident only reads. It
works like Codex's `auto_review`, a reviewer looking at the request in
front of it, but it never touches Claude Code's own permission settings and
it can only ever say "let this one through," never "deny": off, or for a call
it is not sure of, a pane behaves exactly as it always would. Today that
reviewer is a fixed policy: a `Bash` call is let through only when it is a
single call to a command that only ever reads (`git status`, `cat`, `grep`
and the like) inside the project, with nothing in it that could chain into
something else or write anywhere; a call that changes a file, reads outside
the project, or reads a secret file (a `.env`, a private key, a credentials
file) is always left to ask. A `git` command is let through only when git
itself, asked where the command will run, has nothing configured that would
run a program or reach the network, and `git status`, `git diff` or `git
describe` never in a repository with a submodule in it. A command too long to
be judged whole is always left to ask.

The details of that policy:

- The commands let through are a short list of ones that only read (`git status`,
  `git log`, `cat`, `grep` and the like). A flag that makes one of them write or
  run something, such as `git diff --output`, sends the call back to ask.
- No `go` command is let through, because `go env` writes and any `go` command
  may download the toolchain `go.mod` names.
- A command over 4 KiB is asked about whatever it says, so it is never judged by
  its beginning alone.
- No argument may name a path outside the project: an absolute path, one under
  `~`, or one that climbs out with `..`. Claude Code would have asked before
  reading there. The shell rewrites quotes, backslashes and globs before a command
  sees them, so a command holding any of those is asked about too.
- No argument may name a file whose contents are a secret in their own right: a
  `.env` file, a private key, an `.npmrc`, `.netrc` or `.pgpass` file, or a
  credentials file. A read of one would otherwise reach the transcript unseen. The
  public half of a key pair (`id_rsa.pub`) is left alone.
- A `git` command is let through only once git itself, asked in the directory the
  command will run in, reports nothing configured that would run a program or
  reach the network: no `core.fsmonitor` command, external diff, textconv or
  filter driver (Git LFS's own is excepted), gpg program, trace2 target or
  partial-clone remote. That holds whether the setting comes from the repository,
  a file it includes, or your own global configuration.
- `git status`, `git diff` and `git describe` are asked about in any repository
  with a submodule in it, since each runs git again inside the submodule under
  that submodule's own configuration, which two ordinary files an agent writes
  into the project are enough to supply.
- Anything that changes a file (`Edit`, `Write`, `MultiEdit`) is left to ask every
  time, on purpose. That is the kind of call a person is meant to see before it
  happens.

These path checks read the command as text, not the filesystem. Auto-approve
read-only is not a sandbox. A symlink inside the project that points outside it reads as
an ordinary in-project name and is let through, and the secret-file check
knows only conventional names. Both err towards reading, which is part of why
it is off unless you turn it on, and why you should turn it on only
for a pane whose agent you trust.

It is off for a pane unless you turn it on, with the ✓ in the pane's header,
or for new panes with **Settings › General › Auto-approve read-only
in new panes**; a pane started from another one (a
`flockdeck spawn` helper) starts as that pane is.
A pane keeps its setting across restarts, whichever way you left it.

## Reading Flockdeck mail

Reading Flockdeck mail is allowed in every pane, whether or not this switch is
on, and it does not count towards the pane's auto-approved calls. Only these
forms are allowed, written exactly so:

- `flockdeck inbox`, with `-all` and `-json` each at most once
- `flockdeck inbox read <id>`, with `-json` at most once and an id of one to
  nine digits

Anything else is asked about as usual: `-wait`, `-follow`, any other flag or
extra word, sending a message (`flockdeck msg send`), every other subcommand,
a path to the program (`./flockdeck`), a variable set in front of it, a pipe,
a quote, or a command over 4 KiB. It is also asked about when the folder the
command runs in holds a file called `flockdeck`, since the shell could run that
instead. Words must be separated by single spaces.

This works for agents that report their tool calls to Flockdeck (Claude Code)
and for the built-in chat client. Other command-line agents have approval
modes of their own, which Flockdeck does not change; their briefings say how to
allow `flockdeck inbox`.
