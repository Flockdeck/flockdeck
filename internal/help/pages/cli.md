# The command line

Once it is running you rarely need the command line: projects are opened and
switched from inside the window. These are what is left.

| Command | What it does |
| --- | --- |
| `flockdeck` | Open the current directory |
| `flockdeck -C ~/code/api` | Open that directory, in the running instance if there is one |
| `flockdeck -new` | Start without the saved layout, and replace it with this run's |
| `flockdeck -shell` | Make the first pane a shell, not an agent |
| `flockdeck -agent codex` | Make every new pane this run that agent |
| `flockdeck -detach` | Run with no window; attach to it later |
| `flockdeck -quit` | Stop a running instance and its agents |
| `flockdeck -no-window` | Serve headless, no browser needed here; print the URL and open it yourself |
| `flockdeck -solo` | Start a separate instance instead of attaching |
| `flockdeck -version` | Print the version |
| `flockdeck agents` | List the agents and models that `-agent` and `spawn` accept, and which are installed here |

`-no-window` needs no browser on the machine it runs on either, so it's
equally at home on a server you own — see **Self-hosted** in
[Remote access](#remote) for running Flockdeck headless and reaching it from
a paired phone or laptop.

Running the binary again does **not** start a second set of agents. It finds
the instance already going, hands it the directory you asked for, and opens a
window onto it. `-solo` is the escape hatch when you genuinely want two.

`-new` is not a way to glance at an empty window. Its layout is saved over the
one it skipped within half a minute of starting, and again when the run ends.
The other projects you had open are put back in the list when the run ends,
so the next start reopens them as before, around the one this run was started
on, which is where it lands. One you closed during the run stays closed. A run that crashes after its window was reloaded
or detached has already written the list without them, and the next start
opens only its own projects.

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
first, then in `keys.json` in the state directory, and last in
`FLOCKDECK_API_KEY`. A vendor's own variable, such as `OPENAI_API_KEY`, is read
only while the agent talks to that vendor's own address.

| Command | What it does |
| --- | --- |
| `flockdeck keys set openai` | Reads the key from stdin, so it misses shell history |
| `flockdeck keys list` | Which agents have one, not what it is |
| `flockdeck keys clear openai` | Forgets the one Flockdeck stored |
| `flockdeck keys check openai` | Asks the agent's endpoint whether it takes the key a pane would use |
| `flockdeck keys endpoint <agent> <url>` | Points an API agent at another address; `default` in place of the address goes back to the vendor's own |

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
| `flockdeck remote revoke <id or name>` | Unpair a device |
| `flockdeck remote rename <name>` | Rename this machine; with `-device <id or name>`, a paired device instead |
| `flockdeck remote disable` | Remove this machine from the relay; `-force` if it cannot be reached |
| `flockdeck remote move <relay>` | Enrol with another relay, then leave this one once it answers; takes `-invite`, `-join`, `-name`, and `-yes` to skip the question. Every paired device has to pair again |

A running instance is told when `enable`, `disable` or `move` changes
anything, and connects, disconnects or switches relay on the spot. [Remote access](#remote) has the rest.

## chat

```sh
flockdeck chat
```

This is the terminal chat client Flockdeck runs in a pane for an API agent — the
one that talks to a model API itself, with no wrapper CLI, no node and no
Python. It is told which agent, which model and which session to be; the pane
fills all three in, which is why you meet it as a pane rather than type it. Run
outside a pane it still works, but there is nothing listening for the lifecycle
events it reports, so nothing turns amber when it wants you. It has flags of its
own for setting it up by hand — which wire to speak, the base URL, the model,
and which environment variable holds the key — and lists them when asked for
help, for talking to an endpoint from a plain terminal. On
Windows, run it as `flockdeck-chat chat`: `flockdeck.exe` is built without a
console, so it has nowhere to draw the conversation, and `flockdeck-chat.exe`
beside it is the same program built with one.

## update

| Command | What it does |
| --- | --- |
| `flockdeck update` | Fetches the latest release and puts it in place |
| `flockdeck update -check` | Says whether there is one, and stops |

When a new version has been downloaded, an **Update** button appears at the
right of the top bar. It offers **Restart now**, which saves and reopens your
layout but stops the running agents, or **Later**, which installs it when
Flockdeck next quits. A window reached through the relay is not shown the
button, and cannot turn the check for updates on or off: both are done at the
desk.

Releases are published at `dl.flockdeck.ai` as one archive per platform, with
a `checksums.txt` beside them, signed with the release key that is built into
Flockdeck. A second, standby key, held offline and used only if the first is
ever lost or compromised, has been trusted alongside it since v0.3.5. GitHub
carries every release too, signed the same way: when `dl.flockdeck.ai` cannot
be reached, or what it serves is not signed by a trusted key, Flockdeck
downloads from GitHub instead and says why in its log.
Wherever it comes from, the download is checked against its signed SHA-256
before anything is replaced, and a download that does not match is thrown away
rather than installed.

Which release is the latest comes from `latest.json` on `dl.flockdeck.ai`,
which only names a version; Flockdeck then reads that version's own signed
manifest. So an out-of-date `latest.json` can hold an update back for a few
minutes, but it can never have anything unsigned or older installed.

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
| `FLOCKDECK_LAUNCH` | Which start of the pane this is — set for you, sent back by its hooks so a late one from before a restart is dropped |

`FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME` and `FLOCKDECK_PROJECT` are what a shell pane —
which has no lifecycle hooks of its own — has to go on.

`FLOCKDECK_API`, `FLOCKDECK_TOKEN`, `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME` and
`FLOCKDECK_PROJECT` were called `PERCH_*` before the program was renamed. Panes
still carry both spellings of those five and `spawn` still reads both, so a
prompt or a script written against the old names keeps working; they will go
in a later release. `FLOCKDECK_AGENT`, `FLOCKDECK_MODEL` and `FLOCKDECK_LAUNCH`
are newer and have only the one name.
