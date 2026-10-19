# CLI commands

* [flockdeck agents](agents.md) - Prints the catalog of agents Flockdeck can run, with models, install state and roles.
* [flockdeck baton](baton.md) - Lists and prints batons, the handoff documents agents write for each other.
* [flockdeck chat](chat.md) - Flockdeck's own chat client, which a pane running an API agent starts.
* [flockdeck close](close.md) - Closes another pane, or every finished pane, from inside a pane.
* [flockdeck](flockdeck.md) - The root command: opens the workspace and window, or dispatches to a subcommand.
* [flockdeck helpers](helpers.md) - Installs, uninstalls, starts, stops and opens helper apps.
* [flockdeck hook](hook.md) - Hidden subcommand a Claude Code pane runs on each lifecycle event to report back to the instance.
* [flockdeck inbox](inbox.md) - Lists, waits for, streams and reads the messages other agents sent to this pane.
* [flockdeck keys](keys.md) - Stores, clears and checks the API keys that API agents use, and sets their endpoints.
* [flockdeck msg](msg.md) - Sends a message to another agent in the same project.
* [flockdeck peer-name](peer-name.md) - Tells the instance the name another Claude session would use to address this pane.
* [flockdeck recordings](recordings.md) - Lists recorded pane transcripts and exports an agent's stored conversation as one.
* [flockdeck remote pair](remote-pair.md) - Prints a one-time link and QR code that pairs a device, or a code that enrols another machine.
* [flockdeck remote](remote.md) - Enrols this machine with a relay so other devices can reach its agents, and manages pairing, naming and removal.
* [flockdeck spawn](spawn.md) - Starts another agent or a shell from inside a pane, optionally in a worktree, a named tab or from a baton.
* [flockdeck statusline](statusline.md) - Hidden bridge that reports a Claude pane's cost and rate limits to the instance and runs the user's own status line.
* [flockdeck task](task.md) - The project's shared task board, used by the agents of a team.
* [flockdeck team](team.md) - Lists the agent panes of the calling pane's project with role, name, agent and status.
* [flockdeck update](update.md) - Fetches the latest release, or a named one, verifies it and puts it in place.
