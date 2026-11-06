# Notes for the release that carries the new window

This is not a release file: it is the text to fold into the notes of the first release that ships the team-graph window as the only window. Copy it into `docs/releases/vX.Y.Z.md` when the tag is chosen, and delete this file.

## A new window

The window is now the team graph. Panes are nodes in a graph of who is working with whom, what each is doing and what is waiting on you. A pane opens in a drawer with its terminal or its conversation, a Changes tab, its task and its worktree. The old tiled window is gone: this release does not have it.

## Removed: flockdeck chat and the API agents

`flockdeck chat` and the agents that ran on it are gone (#705, API 1.15): Anthropic, OpenAI, Google and OpenAI-compatible endpoints. This is a breaking change. An `agents.json` entry that sets `api`, or that names one of the removed built-ins, is dropped with a notice. The command `setAgentAddress` and the message `agentAddress` are removed, and a server answers `setAgentAddress` as `unsupported`. The per-token model prices left the agent list too. Use a command-line agent such as Claude Code, Codex or Gemini CLI. `flockdeck keys` stays: a key you store is given to a structured agent that takes one.

Structured panes (agents run over the Agent Client Protocol) stay a beta. They are off until `"structuredPanes": true` is in `prefs.json` in the state directory.

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
