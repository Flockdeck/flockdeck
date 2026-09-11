# Agents and models

Every agent pane runs a coding agent of your choosing at a model of your
choosing. Claude Code is the default and behaves exactly as it always has; the
picker is how you reach anything else.

## Picking one

The plain [[key:newAgentTab]] and [[key:splitRight]] stay one keystroke and
take the default agent, because that is what you want almost every time. To
choose deliberately, use [[action:newAgentTabChoose]] or
[[action:splitRightChoose]] — the same picker opens when you hold the split
button in a pane header, or click the caret beside **New tab**.

The picker lists agents in two groups, **installed** and **not installed**. An
agent you have not got is shown greyed with where to get it rather than
hidden: somebody who has never installed Codex should still learn that Flockdeck
would run it. Expand an agent for its models, with its default marked. **Set
as default for this project**, at the foot of the picker, makes the choice
stick for this project, so the one-keystroke split keeps doing the right thing.

The pane header names what is running, beside the branch and in the same dim
weight — `claude · sonnet`, `codex · gpt-5`. A shell pane shows nothing there,
because there is nothing to choose.

## Two kinds of agent

**CLI agents** are the tools you would run in a terminal yourself: Claude Code,
Codex, Gemini, Aider, opencode, Cursor's agent. Flockdeck runs the command in the
pane's pseudo-terminal, so it behaves exactly as it does anywhere else and uses
whatever login that tool already has. Nothing is installed for you; an agent
whose command is not on your `PATH` is the greyed kind.

**API agents** talk to a model API directly. There is no wrapper CLI, no node
and no Python: Flockdeck runs its own chat client, `flockdeck chat`, in the pane. It is
a real terminal chat client — streamed answers, a status line carrying the
model and the running cost, and tools for reading files, editing them and
running commands, the last of which ask before they act. Anthropic, OpenAI and
Google are built in, and so is a plain OpenAI-compatible endpoint, which is how
a local server — Ollama, LM Studio, vLLM — or a gateway becomes an agent.

## Not every agent can do everything

Most of what Flockdeck does beyond drawing a terminal depends on the agent
cooperating, and they do not all cooperate in the same ways.

| If the agent | Then |
| --- | --- |
| reports its own lifecycle | its status dot says what it is really doing, rather than what its output looks like |
| can resume by id | restoring a layout brings its conversation back, not only its pane |
| writes a transcript | fan out reads its plan from what it wrote, and it is listed in past conversations |
| has a trust question | a fan-out can answer it ahead of time for the worktrees it cuts |
| answers a start-up hook | its briefing survives a compaction, rather than being said once and summarised away |

An agent that does none of it still works perfectly well: it is a terminal with
a program in it, which is where every one of these features started. Where a
capability is missing Flockdeck falls back rather than failing — status comes from
the terminal bell, a quiet timer and the lines the agent prints; fan out reads
the screen; resume is not attempted, and the pane starts fresh.

## Keys, for the API agents

A CLI agent uses the login it already has, and Flockdeck never sees it. An API
agent needs a key, which is looked for in that agent's own environment
variables first — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and the rest — and then
in Flockdeck's own store.

```sh
flockdeck keys set openai    # reads the key from stdin, so it misses shell history
flockdeck keys list          # which agents have one, not what it is
```

Keys are kept in `keys.json` in the state directory, readable only by you. A
key reaches exactly one place: the environment of the chat process for the pane
that needs it. It is never logged, never written into a saved layout and never
shown — the interface says **set** or **not set**, offers *set…* and *clear*,
and will not read one back to you. An endpoint that needs no key at all, such
as a local server on loopback, counts as available without one.

## Adding your own: agents.json

The picker is the built-in agents overlaid with your own file, `agents.json` in
the state directory — `%AppData%\flockdeck` on Windows, `~/Library/Application
Support/flockdeck` on macOS, `~/.config/flockdeck` on Linux.

```json
{
  "version": 1,
  "defaults": { "agent": "claude", "model": "" },
  "projects": { "C:\\code\\api": { "agent": "codex", "model": "gpt-5" } },
  "agents": [
    { "id": "claude", "defaultModel": "sonnet" },
    { "id": "local", "name": "Local llama", "runner": "api",
      "api": { "wire": "openai", "baseURL": "http://127.0.0.1:11434/v1" },
      "models": [{ "id": "qwen3-coder" }] }
  ]
}
```

Entries are matched to the built-ins by `id` and merged field by field: the
`claude` entry above changes its default model and leaves everything else
alone. An `id` matching no built-in is an agent of your own. `"hidden": true`
takes one out of the picker without removing it.

The file is read fresh every time the picker opens, so editing it by hand takes
effect without a restart. A file that does not parse is a notice in the
interface and nothing worse — the built-in agents carry on, because a typo in a
settings file is not a reason to be unable to start work.
