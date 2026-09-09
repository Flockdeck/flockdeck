# The command line

Once it is running you rarely need the command line: projects are opened and
switched from inside the window. These are what is left.

```sh
perch                 # open the current directory
perch -C ~/code/api   # …or attach to a running instance and open it there
perch -new            # ignore the saved layout
perch -shell          # first pane is a shell, not an agent
perch -detach         # run with no window; attach to it later
perch -quit           # stop a running instance and its agents
perch -no-window      # just serve; print the URL and open it yourself
perch -solo           # start a separate instance instead of attaching
perch -version        # print the version
```

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it. `-solo` is the escape hatch when you genuinely want two.

## spawn

Run from inside a pane, this starts another agent:

```sh
perch spawn [--worktree <branch>] [--split] [--shell] <task>
```

The address and token come from the environment the pane was started with —
`PERCH_API` and `PERCH_TOKEN` below — so only processes running inside a pane
can use it, and running it anywhere else says so rather than failing
obscurely. That is what lets an agent hand work to helpers of its own.

## keys

An API agent — one Perch talks to directly rather than through a CLI of its
own — needs a key. It is looked for in that agent's own environment variables
first, and then in `keys.json` in the state directory.

```sh
perch keys set openai    # reads the key from stdin, so it misses shell history
perch keys list          # which agents have one, not what it is
```

Nothing here ever prints a key back, and neither does the interface.

## chat

```sh
perch chat
```

This is the terminal chat client Perch runs in a pane for an API agent — the
one that talks to a model API itself, with no wrapper CLI, no node and no
Python. It is told which agent, which model and which session to be; the pane
fills all three in, which is why you meet it as a pane rather than type it. Run
outside a pane it still works, but there is nothing listening for the lifecycle
events it reports, so nothing turns amber when it wants you.

## Environment

| Variable | Effect |
| --- | --- |
| `PERCH_BROWSER` | Force which browser provides the window |
| `PERCH_API` | Where Perch listens for its panes — set for you |
| `PERCH_TOKEN` | The secret that goes with it — set for you |
| `PERCH_PANE` | The pane's id — set for you, read by `spawn` |
| `PERCH_PANE_NAME` | The pane's name, for a shell prompt to use |
| `PERCH_PROJECT` | The project the pane belongs to |
| `PERCH_AGENT` | Which agent the pane is running |
| `PERCH_MODEL` | Which model it was asked for, if any |

`PERCH_PANE`, `PERCH_PANE_NAME` and `PERCH_PROJECT` are what a shell pane —
which has no lifecycle hooks of its own — has to go on.

These were called `AGENT_WRAPPER_*` before the program was renamed. Panes still
carry both spellings and `spawn` still reads both, so a prompt or a script
written against the old names keeps working; they will go in a later release.
