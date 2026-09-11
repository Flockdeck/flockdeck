# The command line

Once it is running you rarely need the command line: projects are opened and
switched from inside the window. These are what is left.

```sh
flockdeck                 # open the current directory
flockdeck -C ~/code/api   # …or attach to a running instance and open it there
flockdeck -new            # ignore the saved layout
flockdeck -shell          # first pane is a shell, not an agent
flockdeck -detach         # run with no window; attach to it later
flockdeck -quit           # stop a running instance and its agents
flockdeck -no-window      # just serve; print the URL and open it yourself
flockdeck -solo           # start a separate instance instead of attaching
flockdeck -version        # print the version
```

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it. `-solo` is the escape hatch when you genuinely want two.

## spawn

Run from inside a pane, this starts another agent:

```sh
flockdeck spawn [--worktree <branch>] [--split] [--shell] <task>
```

The address and token come from the environment the pane was started with —
`FLOCKDECK_API` and `FLOCKDECK_TOKEN` below — so only processes running inside a pane
can use it, and running it anywhere else says so rather than failing
obscurely. That is what lets an agent hand work to helpers of its own.

## keys

An API agent — one Flockdeck talks to directly rather than through a CLI of its
own — needs a key. It is looked for in that agent's own environment variables
first, and then in `keys.json` in the state directory.

```sh
flockdeck keys set openai    # reads the key from stdin, so it misses shell history
flockdeck keys list          # which agents have one, not what it is
```

Nothing here ever prints a key back, and neither does the interface.

## chat

```sh
flockdeck chat
```

This is the terminal chat client Flockdeck runs in a pane for an API agent — the
one that talks to a model API itself, with no wrapper CLI, no node and no
Python. It is told which agent, which model and which session to be; the pane
fills all three in, which is why you meet it as a pane rather than type it. Run
outside a pane it still works, but there is nothing listening for the lifecycle
events it reports, so nothing turns amber when it wants you.

## update

```sh
flockdeck update          # fetch the latest release and put it in place
flockdeck update -check   # say whether there is one, and stop
```

Releases are published on GitHub as one archive per platform, with a
`checksums.txt` beside them. The download is checked against its published
SHA-256 before anything is replaced, and a download that does not match is
thrown away rather than installed.

Replacing the binary does not disturb an instance that is already running: it
is running from an image the operating system already holds, so the new version
is simply what starts next time. The old file is moved aside and swept up by
the following start.

A build you made yourself is stamped `dev` rather than a version, and is never
replaced by a release — there is no sense in which it is behind one.

Set `FLOCKDECK_UPDATE=off` to stop the window checking on its own. The
subcommand still works; it is the background check that goes.

## Environment

| Variable | Effect |
| --- | --- |
| `FLOCKDECK_BROWSER` | Force which browser provides the window |
| `FLOCKDECK_API` | Where Flockdeck listens for its panes — set for you |
| `FLOCKDECK_TOKEN` | The secret that goes with it — set for you |
| `FLOCKDECK_PANE` | The pane's id — set for you, read by `spawn` |
| `FLOCKDECK_PANE_NAME` | The pane's name, for a shell prompt to use |
| `FLOCKDECK_PROJECT` | The project the pane belongs to |
| `FLOCKDECK_AGENT` | Which agent the pane is running |
| `FLOCKDECK_MODEL` | Which model it was asked for, if any |

`FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME` and `FLOCKDECK_PROJECT` are what a shell pane —
which has no lifecycle hooks of its own — has to go on.

These were called `PERCH_*` before the program was renamed. Panes still
carry both spellings and `spawn` still reads both, so a prompt or a script
written against the old names keeps working; they will go in a later release.
