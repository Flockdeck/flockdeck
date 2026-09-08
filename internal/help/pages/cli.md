# The command line

Once it is running you rarely need the command line: projects are opened and
switched from inside the window. These are what is left.

```sh
agent-wrapper                 # open the current directory
agent-wrapper -C ~/code/api   # …or attach to a running instance and open it there
agent-wrapper -new            # ignore the saved layout
agent-wrapper -shell          # first pane is a shell, not an agent
agent-wrapper -detach         # run with no window; attach to it later
agent-wrapper -quit           # stop a running instance and its agents
agent-wrapper -no-window      # just serve; print the URL and open it yourself
agent-wrapper -solo           # start a separate instance instead of attaching
agent-wrapper -version        # print the version
```

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it. `-solo` is the escape hatch when you genuinely want two.

## spawn

Run from inside a pane, this starts another agent:

```sh
agent-wrapper spawn [--worktree <branch>] [--split] [--shell] <task>
```

The address and token come from the environment the pane was started with —
`AGENT_WRAPPER_API` and `AGENT_WRAPPER_TOKEN` below — so only processes running
inside a pane can use it, and running it anywhere else says so rather than
failing obscurely. That is what lets an agent hand work to helpers of its own.

## Environment

| Variable | Effect |
| --- | --- |
| `AGENT_WRAPPER_BROWSER` | Force which browser provides the window |
| `AGENT_WRAPPER_API` | Where the wrapper listens for its panes — set for you |
| `AGENT_WRAPPER_TOKEN` | The secret that goes with it — set for you |
| `AGENT_WRAPPER_PANE` | The pane's id — set for you, read by `spawn` |
| `AGENT_WRAPPER_PANE_NAME` | The pane's name, for a shell prompt to use |
| `AGENT_WRAPPER_PROJECT` | The project the pane belongs to |

The last three are what a shell pane — which has no lifecycle hooks of its
own — has to go on.
