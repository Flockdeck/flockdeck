# Notes for the release that carries the new window

This is not a release file: it is the text to fold into the notes of the first release that ships the team-graph window as the only window. Copy it into `docs/releases/vX.Y.Z.md` when the tag is chosen, and delete this file.

## A new window

The window is now the team graph. Panes are nodes in a graph of who is working with whom, what each is doing and what is waiting on you. A pane opens in a drawer with its terminal or its conversation, a Changes tab, its task and its worktree. The old tiled window is gone: this release does not have it.

## Removed: flockdeck chat and the API agents

`flockdeck chat` and the agents that ran on it are gone (#705, API 1.15): Anthropic, OpenAI, Google and OpenAI-compatible endpoints. This is a breaking change. An `agents.json` entry that sets `api`, or that names one of the removed built-ins, is dropped with a notice. The command `setAgentAddress` and the message `agentAddress` are removed, and a server answers `setAgentAddress` as `unsupported`. The per-token model prices left the agent list too. Use a command-line agent such as Claude Code, Codex or Gemini CLI. `flockdeck keys` stays: a key you store is given to a structured agent that takes one.

## Structured panes are the default for new Claude Code and opencode panes

A new pane of Claude Code or opencode now starts as a structured pane: Flockdeck drives the agent over the Agent Client Protocol and the drawer shows the conversation, instead of running the agent's terminal. The word "beta" is gone from the setting and the help.

This changes new panes only. A saved layout restores each pane as it was, so a terminal pane is still a terminal pane.

On a machine with paired devices or remote access turned on, new panes stay terminal by default. A phone is told when a structured agent is waiting ("Answer on the desktop") but cannot answer it yet, and phone Allow and Deny will not be offered until the phone app ships. Settings, General, "New panes start as" overrides the default either way.

Going back to a terminal:

- Settings, General, "New panes start as": Terminal. Running panes are not ended.
- `flockdeck spawn -terminal` starts the terminal version of an agent from the command line.
- The picker lists both. The terminal entries are named "Claude Code (terminal)" and "opencode (terminal)".
- A structured pane that failed to start, or needs signing in, has a "Restart as terminal" button. The pane keeps its place and folder; the conversation starts afresh.

Agents with no route over ACP stay in a terminal: Cursor Agent, Aider, plain shells, and any CLI you added in `agents.json`. Codex and Gemini CLI stay terminal too, for now. Their structured entries are hidden until they have been run against the real programs.

What a structured pane does not do yet:

- A phone cannot answer a permission request. It gets a notification that says to answer on the desktop.
- Rewind takes back the conversation, not the files the agent changed. File rewind is not built.
- "Always allow" lasts for the session. After a pane restarts the agent asks again, and Flockdeck restarts panes on update.
- Agents get mail as prompts and send with `send_message` and `list_peers`. `flockdeck msg`, `inbox` and `wake` work in terminal panes only.
- A structured pane is not recorded.

Signing in: a Claude Code pane that is not signed in shows "needs setup". Open Agent info, press Sign in, and Flockdeck starts Claude's own login and restarts the pane when it ends. Flockdeck never sees the password or key.

Project trust: in a folder you have not told Claude Code to trust, the adapter does not apply the project's allow rules, and the pane says so. Open the folder in a terminal pane once and accept the trust prompt, or use the permission cards.

Also in this release: forking a conversation after a rewind works with the session file Claude keeps, and the plan checklist draws from Claude's task tools.

## What is missing

These are things the old window did that the new one does not, and they are chosen, not forgotten:

- The GitHub view (pull requests, issues, check runs). Use `gh` in a terminal pane for now.
- Recording a structured pane. Terminal panes record as before.
- A team view in the phone app. The phone still shows the roster.
- The tips bar.
- Tiling, splitting, moving and broadcasting to panes. A team is a graph, and a manager sends to its team.

Every control-socket command the old window used is still in the desktop, so these views can be added back without a server change.

## What was and was not tested

The new window was tested on Windows. It was not run on Linux or macOS, not with the race detector, and no real phone was paired through the relay to it. A relay window was checked to boot and show a terminal against a fixture.

The page is larger than the old plan assumed. On the first-load model in `docs/first-load.md` (a 1.6 Mbit/s link, 150 ms round trip, CPU slowed four times) the shell as it is now takes about 10 seconds to a first meaningful paint, against a 3 second budget. That model is a guess at a mid-range phone, and the real number needs a real phone.
