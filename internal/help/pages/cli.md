# The command line

Once it is running you rarely need the command line: projects are opened and
switched from inside the window. These are what is left.

| Command | What it does |
| --- | --- |
| `flockdeck` | Open the current directory |
| `flockdeck -C ~/code/api` | Open that directory, in the running instance if there is one |
| `flockdeck -new` | Start without the saved layout, and replace it on exit |
| `flockdeck -shell` | Make the first pane a shell, not an agent |
| `flockdeck -agent codex` | Make every new pane this run that agent |
| `flockdeck -detach` | Run with no window; attach to it later |
| `flockdeck -quit` | Stop a running instance and its agents |
| `flockdeck -no-window` | Just serve; print the URL and open it yourself |
| `flockdeck -solo` | Start a separate instance instead of attaching |
| `flockdeck -version` | Print the version |

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it. `-solo` is the escape hatch when you genuinely want two.

`-new` is not a way to glance at an empty window. When that run ends, its
layout is saved over the one it skipped, and the list of other open projects is
replaced too, so they are not reopened next time either — their own layouts are
kept, and come back when you open them.

## spawn

Run from inside a pane, this starts another agent:

```sh
flockdeck spawn [--worktree <branch>] [--split] [--shell]
                [--agent <id>] [--model <model>] <task>
```

`--agent` and `--model` choose which agent the helper is; without them it is
whatever the project runs by default. `flockdeck agents` lists the names both
of them take, and a name neither the catalog nor the agent has is answered here
rather than becoming a pane that never starts.

The address and token come from the environment the pane was started with —
`FLOCKDECK_API` and `FLOCKDECK_TOKEN` below — so only processes running inside a pane
can use it, and running it anywhere else says so rather than failing
obscurely. That is what lets an agent hand work to helpers of its own.

## keys

An API agent — one Flockdeck talks to directly rather than through a CLI of its
own — needs a key. It is looked for in that agent's own environment variables
first, and then in `keys.json` in the state directory.

| Command | What it does |
| --- | --- |
| `flockdeck keys set openai` | Reads the key from stdin, so it misses shell history |
| `flockdeck keys list` | Which agents have one, not what it is |
| `flockdeck keys clear openai` | Forgets the one Flockdeck stored |

Nothing here ever prints a key back, and neither does the interface.

## remote

Reach this machine's agents from another device, through a relay.

| Command | What it does |
| --- | --- |
| `flockdeck remote enable` | Enrol this machine; takes `-relay`, `-name`, `-join`, `-invite` |
| `flockdeck remote pair` | A one-time link and QR code that pairs a device |
| `flockdeck remote pair -desktop` | A code that enrols another machine into the account |
| `flockdeck remote status` | Whether it is on, and whether it is connected |
| `flockdeck remote devices` | What is paired, with the ids `revoke` takes |
| `flockdeck remote revoke <id>` | Unpair a device |
| `flockdeck remote disable` | Remove this machine from the relay; `-force` if it cannot be reached |

A running instance is told when `enable` or `disable` changes anything, and
connects or disconnects on the spot. [Remote access](#remote) has the rest.

## chat

```sh
flockdeck chat
```

This is the terminal chat client Flockdeck runs in a pane for an API agent — the
one that talks to a model API itself, with no wrapper CLI, no node and no
Python. It is told which agent, which model and which session to be; the pane
fills all three in, which is why you meet it as a pane rather than type it. Run
outside a pane it still works, but there is nothing listening for the lifecycle
events it reports, so nothing turns amber when it wants you. `flockdeck chat -h`
lists the flags that set it up by hand — `-wire`, `-base-url`, `-model`,
`-key-env` among them — for talking to an endpoint from a plain terminal. On
Windows, run it as `flockdeck-chat chat`: `flockdeck.exe` is built without a
console, so it has nowhere to draw the conversation, and `flockdeck-chat.exe`
beside it is the same program built with one.

## update

| Command | What it does |
| --- | --- |
| `flockdeck update` | Fetches the latest release and puts it in place |
| `flockdeck update -check` | Says whether there is one, and stops |

Releases are published on GitHub as one archive per platform, with a
`checksums.txt` beside them. The download is checked against its published
SHA-256 before anything is replaced, and a download that does not match is
thrown away rather than installed.

Replacing the binary does not disturb an instance that is already running: it
is running from an image the operating system already holds, so the new version
is simply what starts next time. The old file is moved aside and swept up by
the following start.

A build you made yourself — stamped `dev` by `go build` or `go install`, or by
`git describe` when built with make — is never replaced by a release: there is
no sense in which it is behind one.

Set `FLOCKDECK_UPDATE=off` to stop the window checking on its own, and to stop
an update it has already downloaded being put in place when Flockdeck exits.
The subcommand still works; it is the background updating that goes.

## Environment

| Variable | Effect |
| --- | --- |
| `FLOCKDECK_BROWSER` | Force which browser provides the window |
| `FLOCKDECK_UPDATE` | `off` stops updating in the background: no checks, and nothing already downloaded is put in place |
| `FLOCKDECK_RELAY` | Which relay `flockdeck remote enable` uses when `-relay` is not given |
| `FLOCKDECK_API` | Where Flockdeck listens for its panes — set for you |
| `FLOCKDECK_TOKEN` | The secret that goes with it — set for you |
| `FLOCKDECK_PANE` | The pane's id — set for you, read by `spawn` |
| `FLOCKDECK_PANE_NAME` | The pane's name, for a shell prompt to use |
| `FLOCKDECK_PROJECT` | The project the pane belongs to |
| `FLOCKDECK_AGENT` | Which agent the pane is running |
| `FLOCKDECK_MODEL` | Which model it was asked for, if any |

`FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME` and `FLOCKDECK_PROJECT` are what a shell pane —
which has no lifecycle hooks of its own — has to go on.

`FLOCKDECK_API`, `FLOCKDECK_TOKEN`, `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME` and
`FLOCKDECK_PROJECT` were called `PERCH_*` before the program was renamed. Panes
still carry both spellings of those five and `spawn` still reads both, so a
prompt or a script written against the old names keeps working; they will go
in a later release. `FLOCKDECK_AGENT` and `FLOCKDECK_MODEL` are newer and have
only the one name.
