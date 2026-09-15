# Agents and models

Every agent pane runs a coding agent of your choosing at a model of your
choosing. Claude Code is the default and behaves exactly as it always has; the
picker is how you reach anything else.

## Picking one

The plain [[key:newAgentTab]] and [[key:splitRight]] stay one keystroke and
take the default agent, because that is what you want almost every time. To
choose deliberately, use [[action:newAgentTabChoose]] or
[[action:splitRightChoose]] from the command palette, or click the `▾` beside
the `+` that opens a new tab.

The picker lists agents in two groups, **installed** and **not installed**. An
agent you have not got is shown greyed with where to get it rather than
hidden: somebody who has never installed Codex should still learn that Flockdeck
would run it. Expand an agent for its models, with its default marked. Each
model carries its tier — **small**, **mid** or **top**, how capable and so how
costly it is among that agent's own — and a model of an API agent its published
price per million tokens, in and out, with the day it was read. A command-line
agent's models show no price: the same model may be billed per token or
covered by a subscription, and only you know which. Every price comes from one
table compiled into Flockdeck, each rate with the day it was read from the
provider's own pricing page. It changes with releases; nothing is fetched. The
chat's status line and the estimates in the pane header are priced from the
same table. At the
foot of the picker, **Set as default for** makes the choice stick, either for
this project or for every project, so the one-keystroke split keeps doing the
right thing. **Use the default for every project** removes this project's own
choice.

The pane header names what is running, beside the branch and in the same dim
weight — `claude · sonnet`, `codex · gpt-5.6-sol`. A shell pane shows nothing there,
because there is nothing to choose. For the API agents, and for Claude Code
where its status line is read, what the conversation has spent and how near its
usage limit it is follow;
[Spend and limits](#spend) explains the figures.

## Two kinds of agent

**CLI agents** are the tools you would run in a terminal yourself: Claude Code,
Codex, Gemini, Aider, opencode, Cursor's agent. Flockdeck runs the command in the
pane's pseudo-terminal, so it behaves exactly as it does anywhere else and uses
whatever login that tool already has. Nothing is installed for you; an agent
whose command is not on your `PATH` is the greyed kind.

**API agents** talk to a model API directly. There is no wrapper CLI, no node
and no Python: Flockdeck runs its own chat client, `flockdeck chat`, in the pane. It is
a real terminal chat client — streamed answers, a status line carrying the
model, its token counts and, for a model in the price table, the running cost,
and tools for reading files, editing them and
running commands; the ones that write a file or run a command ask before they
act. A command can be let through for the rest of the session by answering
*always*; a write is asked about every time. *Always* is not offered for a
command that can run anything at all — a shell, `git config`, `npm exec`,
`docker run` — and a command with an option that writes a file wherever it
says or runs a program it names, such as `git log --output` or
`go test -exec`, is asked about even after *always*. Anthropic, OpenAI and
Google are built in, and so is a plain OpenAI-compatible endpoint, which is how
a local server — Ollama, LM Studio, vLLM — or a gateway becomes an agent.

The price table holds Anthropic's, OpenAI's and Google's models. A model is
priced only when its id is one the table names, or a dated snapshot of one
such as `claude-haiku-4-5-20251001`; any other id, including one that only
begins like a priced model, is shown in tokens with no dollars, because a
made-up price is worse than none.

## An endpoint's address

The OpenAI-compatible endpoint ships with no address, because none would be
right for everybody, so it waits under **not installed** until it has one.
Pick it and the picker asks for the address there and then: type where the
model server answers, starting `http://` or `https://` — `http://127.0.0.1:11434/v1`
for Ollama, `http://127.0.0.1:1234/v1` for LM Studio — and press `Enter`.
`Escape` puts the field away without saving anything.

How much of the address to give depends on the path. For an OpenAI-compatible
endpoint, `/v1` is added only to a bare address such as
`http://127.0.0.1:11434`. An address with a path of its own — `.../v1`, or a
gateway's `https://gateway.example/openai` — is taken as the whole of the API's
root, and nothing is added to it. So give the path your server or gateway
documents, `/v1` included where it has one. For the Anthropic and Gemini
agents, the API's version is added unless the address already ends in it. A
request's full address pasted in, ending `/chat/completions`, `/messages` or
`/models`, has that part taken off first.

An address on this machine needs no key, so a local model server is offered
the moment its address is saved. One anywhere else, such as a gateway, needs a
key as well, set under [[action:apiKeys]].

Any other API agent that has been given an address — one of your own, or a
built-in pointed at a proxy — shows it beside its name. Open the agent with the
right arrow and choose its **Address** row to change it. Saved empty, the
address is taken away, and a built-in goes back to its vendor's own.

An address that is not one is refused under the field, with what to type
instead: `localhost:11434` is answered with `http://localhost:11434`. An
address with a name or password in it is refused too, because it is shown in
the picker; the key goes under [[action:apiKeys]]. A pane already running keeps
the address it started with until it is restarted. From a terminal, `flockdeck
keys endpoint <agent> <address>` does the same, and `default` in place of the
address goes back to the vendor's own.

## Not every agent can do everything

Most of what Flockdeck does beyond drawing a terminal depends on the agent
cooperating, and they do not all cooperate in the same ways.

| If the agent | Then |
| --- | --- |
| reports its own lifecycle | its status dot says what it is really doing, rather than what its output looks like |
| can resume by id | restoring a layout brings its conversation back, not only its pane |
| writes a transcript | it can be resumed from it; Claude Code's is also where fan out reads a plan and what past conversations lists |
| has a trust question | a [fan-out](#fanout) can answer it ahead of time for the worktrees it cuts |
| answers a start-up hook | its briefing survives a compaction, rather than being said once and summarised away |

An agent that does none of it still works perfectly well: it is a terminal with
a program in it, which is where every one of these features started. Where a
capability is missing Flockdeck falls back rather than failing — status comes from
the terminal bell and a quiet timer; fan out reads the screen; resume is not
attempted, and the pane starts fresh.

Of the built-in agents, Claude Code and the four API agents report their own
lifecycle and answer the start-up hook. Codex, Gemini CLI, Aider, opencode and
Cursor Agent are read from their terminals, and are not briefed. An entry in
`agents.json` can have the briefing put in front of such an agent's opening
task instead, with `"caps": {"context": "prompt"}`, and can give it
`"patterns"` — `"waiting"` and `"idle"`, each a list of phrases its output
shows in that state — so that its status is read from what it prints as well. They are plain text, not expressions, and
case does not matter; none of the built-ins has any yet.

Aider is handed its opening task with `--message`, which Aider treats as a
single message, so a pane started with a task (by a fan-out, say) may end when
the task does.

## Keys, for the API agents

A CLI agent uses the login it already has, and Flockdeck never sees it. An API
agent needs a key, which is looked for in that agent's own environment
variables first — `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and the rest — then in
Flockdeck's own store, and last in `FLOCKDECK_API_KEY`, which any API agent
reads. A vendor's own variable is read only by an agent talking to that
vendor's own address: a built-in given a gateway's or a proxy's address is
never sent the key you exported for the vendor, and uses the one stored for it.

| Command | What it does |
| --- | --- |
| `flockdeck keys set openai` | Reads the key from stdin, so it misses shell history |
| `flockdeck keys list` | Which agents have one, not what it is |
| `flockdeck keys clear openai` | Forgets the one Flockdeck stored |

Or set them from the window: [[action:apiKeys]], in the command palette, lists
every API agent with **set** or **not set** beside it — and, for one that is
not, the environment variables it would look in — and offers **Set…** and
**Clear**.

Keys are kept in `keys.json` in the state directory, readable only by you. A
key reaches exactly one place: the environment of the chat process for the pane
that needs it. It is never logged, never written into a saved layout and never
shown — the interface will not read one back to you. An endpoint that needs no key at all, such
as a local server on loopback, counts as available without one.

## Adding your own: agents.json

The picker is the built-in agents overlaid with your own file, `agents.json` in
the state directory — `%AppData%\flockdeck` on Windows, `~/Library/Application
Support/flockdeck` on macOS, `~/.config/flockdeck` on Linux.

```json
{
  "version": 1,
  "defaults": { "agent": "claude", "model": "" },
  "projects": {
    "C:\\code\\api": { "agent": "codex", "model": "gpt-5.6-terra" }
  },
  "agents": [
    { "id": "claude", "defaultModel": "sonnet" },
    {
      "id": "local",
      "name": "Local llama",
      "runner": "api",
      "api": {
        "wire": "openai",
        "baseURL": "http://127.0.0.1:11434/v1"
      },
      "models": [{ "id": "qwen3-coder" }]
    }
  ]
}
```

Entries are matched to the built-ins by `id` and merged field by field: the
`claude` entry above changes its default model and leaves everything else
alone. An `id` matching no built-in is an agent of your own. `"hidden": true`
takes one out of the picker without removing it. A model's `"tier"` —
`"small"`, `"mid"` or `"top"` — is set or corrected the same way; anything else
is named in the notice and ignored.

The file is read fresh every time the picker opens, so editing it by hand takes
effect without a restart.

## Routing

A fan-out multiplies whatever its model costs, and many of its tasks are
mechanical: run the tests, rename a symbol, fix a typo. Routing pre-sets each
row of a [fan-out](#fanout) to a model suited to the work — a smaller one for
mechanical work, a stronger one for hard work — and leaves every other row
exactly as it was. It is **off** until you turn it on, in Settings › Agents ›
Routing, for every project or for one.

What it chooses is shown before anything starts. A routed row's model is
pre-set in its select, with a **↘ routed** tag (↗ for a stronger model) whose
tooltip says which rule chose it and, for an API agent, what the two models
cost. Changing the select makes the row yours again, and **Use the run's
model for every task**, beside the line saying how many rows were routed,
does that for all of them. What was shown is what runs. A pane started on a
routed model says so in its header — `claude · haiku ↘` — with the rule in
its tooltip.

Routing moves work between the models of the agent the run is on, and only
between models whose tier it knows. It sends work to another agent only when
you turn on **Let a rule send work to another agent**, in the same place, and
a rule names that agent's `model` and `agent`. A rule like that could send test
runs to a local model through an OpenAI-compatible endpoint, say. Even then,
only a new fan-out row or helper moves, never a conversation already under
way. The other agent has to be installed or keyed and trusted for the project,
and, if it has an address of its own, something has to be answering there. The
fan-out dialog shows the agent a row will run on, and the pane's header names
the agent and model it would otherwise have used. Claude Code's
**Default** is whatever the CLI is set to, which might be its smallest model
or its largest, so routing leaves work on it alone: choose a model for the run
in the fan-out, or make one your default, for routing to choose from it.

**Suggest** and **Automatic** are the same for a fan-out, since the dialog asks
before anything starts either way. **Automatic** also routes a helper an agent
starts with `flockdeck spawn` without `--agent` or `--model`; with
**Suggest** such a helper runs as asked, since there is nobody to show a
suggestion to. **Never go below** keeps routing off the
smaller tiers for a project where the work matters.

**Strategy** decides what happens to a task no rule matches. **Cost-first,
quality-aware** — the default — leaves it exactly where it was, the same as
routing off would for that row. **Minimise cost** routes it too, to the
cheapest model **Never go below** allows on the agent it was already going to
run on, never to another agent. Either strategy, a rule that matches still
decides first and **Never go below** still holds; the strategy only changes
what happens when nothing matches. The row's tooltip says "no rule matched" for
a suggestion made this way, so it is never mistaken for a rule's own reason.

The policy is kept in `agents.json` — and only there, never in a file inside a
repository, so a repository you clone cannot change what your key spends:

```json
{
  "routing": {
    "mode": "suggest",
    "floor": "",
    "strategy": "",
    "rules": [
      { "name": "run the tests", "tier": "small",
        "when": { "task": "^(re-?)?run (the |all )?(unit |integration )?tests?\\b" } },
      { "name": "schema changes", "tier": "top",
        "when": { "files": ["**/migrations/**", "**/*.sql"] } },
      { "name": "design work", "model": "opus", "agent": "claude",
        "when": { "task": "\\bdesign\\b" } }
    ]
  },
  "projects": {
    "C:\\code\\payments": { "routing": { "mode": "suggest", "floor": "mid" } }
  }
}
```

Rules are tried in order and the **first that matches decides**; a task no rule
matches is left alone under `"strategy": "balanced"` (the default, same as
leaving `strategy` out), or routed to the cheapest model the floor allows
under `"strategy": "cost"`. Every condition in `when` must hold: `task` is a
regular expression matched without regard to case; `minWords` and `maxWords`
bound the task's length; `files` are globs matched against the paths the task
names (`**` is any number of directories, and a glob with no `/` matches a
file's name anywhere); `kind` is `fanout`, `spawn` or `turn`; `agent` is an
agent's id. A rule asks for a `tier`, or for one agent's `model`. Leave
`rules` out for the built-in ones, which Settings lists; `"rules": []` means
none. A project's own policy replaces the one for every project whole. A rule
that cannot be used — a pattern that is not one, a tier that is not one — is
named in the notice and skipped, and the rest still apply.

Saving a default from a Flockdeck that has no routing — 0.2.10 or older —
writes a project's entry back without its `routing`, so a project's policy is
lost that way.

**Routing makes no request.** It decides from these rules alone and
makes no request of any kind. The one exception: with **Let a rule send work
to another agent** on, before moving a row to an agent with an address of its
own, it opens a connection to that address, and closes it at once without
sending anything, to check something is listening. The routed mark on a pane travels only as the
rest of the pane's state does, to your own paired devices when remote access
is on. What it chose, and whether you kept it, is kept
in `routing.jsonl` in the state directory, for your own numbers: the rule's
name and the models, never the task. Settings lists each rule beside how often
the log shows it overridden, once it has decided at least once — the evidence
for deciding which rules are worth hand-editing. Settings has **Clear routing
history**, and to turn all of it off, set every project to **Off**. A file that does not parse is a notice in the
interface and nothing worse — the built-in agents carry on, because a typo in a
settings file is not a reason to be unable to start work.
