# Flockdeck

Flockdeck is a desktop app for running several AI coding agents side by side. Each
agent runs in a real terminal pane, in tabs and split panes. In a git repository
each agent can have its own worktree on its own branch, so agents do not edit each
other's files. Each pane shows whether its agent is working, idle or waiting on
you, so you can tell which one needs you without opening them all.

Claude Code is the default agent. Codex, Gemini CLI, Aider, opencode and Cursor's
agent run the same way, and Flockdeck also has its own chat client for talking to
a model API directly, including a local Ollama or any other OpenAI-compatible
endpoint. A CLI agent uses whatever login it already has. An API agent needs a key,
which `flockdeck keys` stores.

## Install

```sh
curl -fsSL https://flockdeck.ai/install.sh | sh   # macOS and Linux
irm https://flockdeck.ai/install.ps1 | iex        # Windows, in PowerShell
```

The scripts install into a directory you own and need no admin rights. On Linux the
window needs GTK 4 and WebKitGTK (`libgtk-4-1` and `libwebkitgtk-6.0-4` on Debian
and Ubuntu). A CLI agent has to be on your `PATH`, such as
[Claude Code](https://claude.com/claude-code) for the default. The `.deb`, `.rpm`
and Windows installer, `go install`, updating and uninstalling are in
[docs/install.md](docs/install.md). To build from a clone, see
[docs/building.md](docs/building.md).

## Quick start

```sh
flockdeck                 # open the current directory, or the projects open last time
flockdeck -C ~/code/api   # open a project, in the running instance if there is one
flockdeck agents          # the agents it can run, and which are installed here
```

In the window, `Ctrl+Shift+T` opens a new agent tab and `Ctrl+Shift+D` splits the
pane to the right. `Ctrl+Shift+K` opens the command palette, which lists every
action. `Ctrl+Shift+X` turns an agent's plan into several agents, each in its own
worktree when the project is a git repository. `Ctrl+Shift+S` reviews, commits and pushes what an agent changed. `F1`
opens the help. All the shortcuts are in [docs/keys.md](docs/keys.md).

## Flockdeck Remote

Flockdeck Remote is an optional add-on. A paired phone, tablet or browser opens the
full Flockdeck window, terminals included, through Flockdeck's relay. It costs £5 a
month after a 30-day trial. The desktop app is free and never needs it.

```sh
flockdeck remote enable          # enrol this machine with the relay
flockdeck remote pair            # a one-time link and QR code for a device
flockdeck remote remove          # take this machine off; the account stays
flockdeck remote delete-account  # erase the account, its machines and devices
```

This machine connects out to the relay and listens for nothing from the network.
The [Flockdeck Remote page](internal/help/pages/remote.md) says what the relay can
see and how pairing, notifications, unpairing and deleting an account work. A
relay that checks email prints a confirmation code that `flockdeck remote enable`
asks you to enter on the page opened from the emailed link.

## Documentation

Guides in `docs/`:

- [Installing, updating and uninstalling](docs/install.md)
- [Building and working on Flockdeck](docs/building.md)
- [Running Flockdeck on a server](docs/self-hosting.md), including Docker and
  Kubernetes (the [Helm chart README](deploy/helm/flockdeck/README.md) has the chart)
- [Keyboard shortcuts](docs/keys.md)
- [How Flockdeck is built](docs/architecture.md)
- [How each agent learns where it is running](docs/agent-briefing.md)
- [The recording file format](docs/recording-format.md), with its
  [JSON Schema](docs/recording-line.schema.json)
- [The pane status matrix](docs/status-matrix.md)
- [Writing tests that stay isolated](docs/testing.md)

The in-app help, which `F1` opens and docs.flockdeck.ai publishes, is Markdown in
`internal/help/pages`. Placeholders such as `[[key:splitRight]]` in those files are
filled in from the key table when the page is shown.

- [Getting started](internal/help/pages/getting-started.md)
- [Panes and tabs](internal/help/pages/panes.md) and
  [rearranging what is already running](internal/help/pages/rearranging.md)
- [Agents and models](internal/help/pages/agents.md), including routing and API keys
- [Knowing who needs you](internal/help/pages/status.md), including auto-review
  approvals
- [Spend and limits](internal/help/pages/spend.md)
- [Broadcast and the prompt bar](internal/help/pages/broadcast.md)
- [Fan out](internal/help/pages/fanout.md) and [Todo](internal/help/pages/todo.md)
- [Git worktrees](internal/help/pages/worktrees.md),
  [review, commit and push](internal/help/pages/changes.md) and
  [GitHub](internal/help/pages/github.md)
- [Past conversations](internal/help/pages/history.md) and
  [recording a pane](internal/help/pages/recording.md)
- [Handing work to another agent](internal/help/pages/baton.md) and its
  [details](internal/help/pages/baton-details.md)
- [Projects](internal/help/pages/projects.md) and
  [what comes back, and what keeps running](internal/help/pages/persistence.md)
- [Flockdeck Remote](internal/help/pages/remote.md)
- [Settings](internal/help/pages/settings.md),
  [keyboard shortcuts](internal/help/pages/shortcuts.md) and
  [the command line](internal/help/pages/cli.md)
- [When something is wrong](internal/help/pages/troubleshooting.md)

## Sponsoring

Flockdeck is free, with its source available. You can sponsor it to say thank you,
through [GitHub Sponsors](https://github.com/sponsors/jmwri), or the Sponsor button
at the top of this repository. It buys no features, support or priority, and the app
is the same for everyone. Sponsors who ask to be named are listed, by name and a
link, on [the website](https://flockdeck.ai/#sponsor).

## Licence

The desktop app in this repository is released under the [PolyForm
Noncommercial licence](LICENSE): free to use, copy and change for any noncommercial
purpose, source included. The relay, the phone and web client, and the website are
separate, closed-source projects, © 2026 Jim Wright, all rights reserved.

The front end is compiled into the binary and the Go dependencies are linked into
it, so a release carries other people's code as well as this project's. All of the
code is permissive (MIT, ISC, and BSD 2- and 3-Clause), the two typefaces are under
the SIL Open Font License 1.1, and there is no copyleft anywhere. The notices each of
those licences asks for are in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md),
which ships inside every release archive beside the binary.

The agents Flockdeck runs are separate programs and are not redistributed with it.
Claude Code, Codex, Gemini CLI, Aider, opencode and Cursor's agent each keep their
own licences and terms, as do the model APIs the built-in client talks to.
