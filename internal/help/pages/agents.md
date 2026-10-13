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
would run it. Expand an agent for its models, with its default marked. A
model carries its tier where it has a fixed one (**small**, **mid** or **top**: how capable, and so how
costly, it is among that agent's own models). A model of an API agent shows its published
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
weight: `claude · sonnet`, `codex · gpt-5.6-sol`. A shell pane shows nothing there,
because there is nothing to choose. For the API agents, and for Claude Code
where its status line is read, it is followed by what the conversation has
spent and how near its usage limit it is; [Spend and limits](#spend) explains
the figures.

## Two kinds of agent

**CLI agents** are the tools you would run in a terminal yourself: Claude Code,
Codex, Gemini, Aider, opencode, Cursor's agent. Flockdeck runs the command in the
pane's pseudo-terminal, so it behaves exactly as it does anywhere else and uses
whatever login that tool already has. Nothing is installed for you; an agent
whose command is not on your `PATH` is the greyed kind.

**API agents** talk to a model API directly. There is no wrapper CLI, no node
and no Python: Flockdeck runs its own chat client, `flockdeck chat`, in the pane. It is
a real terminal chat client with streamed answers, a status line showing the
model, its token counts and, for a model in the price table, the running cost,
and tools for reading files, editing them and
running commands. The ones that write a file or run a command ask before they
act. A command can be let through for the rest of the session by answering
*always*, and so can edits to files (except inside `.git` or `.hg`). *Always*
is not offered for a
command that can run anything at all (a shell, `git config`, `npm exec`,
`docker run`), and a command with an option that writes a file wherever it
says or runs a program it names, such as `git log --output` or
`go test -exec`, is asked about even after *always*. Anthropic, OpenAI and
Google are built in, and so is a plain OpenAI-compatible endpoint, which is how
a local server (Ollama, LM Studio, vLLM) or a gateway becomes an agent.

The price table holds Anthropic's, OpenAI's and Google's models. A model is
priced only when its id is one the table names, or a dated snapshot of one
such as `claude-haiku-4-5-20251001`; any other id, including one that only
begins like a priced model, is shown in tokens with no dollars, because a
made-up price is worse than none. An API agent given an address of its own,
such as a gateway's, is not priced at all, since what it charges is not the
vendor's list price.

## An endpoint's address

The OpenAI-compatible endpoint ships with no address, because none would be
right for everybody, so it waits under **not installed** until it has one.
Pick it and the picker asks for the address there and then: type where the
model server answers, starting `http://` or `https://` (`http://127.0.0.1:11434/v1`
for Ollama, `http://127.0.0.1:1234/v1` for LM Studio), and press `Enter`.
`Escape` puts the field away without saving anything.

How much of the address to give depends on the path. For an OpenAI-compatible
endpoint, `/v1` is added only to a bare address such as
`http://127.0.0.1:11434`. An address with a path of its own (`.../v1`, or a
gateway's `https://gateway.example/openai`) is taken as the whole of the API's
root, and nothing is added to it. So give the path your server or gateway
documents, `/v1` included where it has one. For the Anthropic and Gemini
agents, the API's version is added unless the address already ends in it. A
request's full address pasted in, ending `/chat/completions`, `/messages` or
`/models`, has that part taken off first.

An address on this machine needs no key, so a local model server is offered
the moment its address is saved. One anywhere else, such as a gateway, needs a
key as well, set under [[action:apiKeys]].

Any other API agent that has been given an address (one of your own, or a
built-in pointed at a proxy) shows it beside its name. Open the agent with the
right arrow and choose its **Address** row to change it. Saved empty, the
address is taken away, and a built-in goes back to its vendor's own.

An address that is not one is refused under the field, with what to type
instead: `localhost:11434` is answered with `http://localhost:11434`. An
address with a name or password in it is refused too, because it is shown in
the picker; the key goes under [[action:apiKeys]]. A pane already running
keeps the address it started with, until the old one cannot be reached: then
it asks the new address instead, and says so. From a terminal, `flockdeck
keys endpoint <agent> <address>` does the same, and `default` in place of the
address goes back to the vendor's own.

## Not every agent can do everything

Most of what Flockdeck does beyond drawing a terminal depends on the agent
cooperating, and they do not all cooperate in the same ways.

| If the agent | Then |
| --- | --- |
| reports its own lifecycle | its status mark says what it is really doing, rather than what its output looks like |
| can resume by id | restoring a layout brings its conversation back, not only its pane |
| writes a transcript | it can be resumed from it; Claude Code's is also where fan out reads a plan and what past conversations lists |
| has a trust question | a [fan-out](#fanout) can answer it ahead of time for the worktrees it cuts |
| answers a start-up hook | its briefing survives a compaction, rather than being said once and summarised away |

An agent that does none of it still works: it is a terminal with
a program in it. Where a
capability is missing Flockdeck falls back rather than failing: status comes from
the terminal bell and a quiet timer; fan out reads the screen; resume is not
attempted, and the pane starts fresh.

Of the built-in agents, Claude Code and the four API agents report their own
lifecycle and answer the start-up hook. Codex, Gemini CLI, Aider, opencode and
Cursor Agent are read from their terminals, but are still briefed: the briefing
is put in front of their opening task, once, and is not refreshed. Codex,
Gemini CLI, opencode and Cursor Agent get it even in a pane opened by hand with
no task; Aider gets it only when there is a task, because `--message` makes it
answer once and exit, and a message that was all briefing would end the pane.
An entry in `agents.json` can choose the same for its own agent with
`"caps": {"context": "prompt"}` (or `"task"` for the Aider behaviour), and can give it
`"patterns"`: `"waiting"` and `"idle"`, each a list of phrases its output
shows in that state, so that its status is read from what it prints as well. They are plain text and
case does not matter. None of the built-ins has any yet.

Aider is handed its opening task with `--message`, which Aider treats as a
single message, so a pane started with a task (by a fan-out, say) may end when
the task does.

## Keys, for the API agents

A CLI agent uses the login it already has, and Flockdeck never sees it. An API
agent needs a key, which is looked for in that agent's own environment
variables first (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and the rest), then in
Flockdeck's own store, and last in `FLOCKDECK_API_KEY`, which any API agent
reads. A vendor's own variable is read only by an agent talking to that
vendor's own address: a built-in given a gateway's or a proxy's address is
never sent the key you exported for the vendor, and uses the one stored for it.

| Command | What it does |
| --- | --- |
| `flockdeck keys set openai` | Reads the key from stdin, so it misses shell history |
| `flockdeck keys list` | Which agents have one, not what it is |
| `flockdeck keys clear openai` | Forgets the one Flockdeck stored |
| `flockdeck keys check openai` | Asks the agent's endpoint whether it takes the key a pane would use |

Or set them from the window: [[action:apiKeys]], in the command palette, or
**API keys** in the settings, lists every API agent with **set** or **not
set** beside it (and, for one that is not, the environment variables it would
look in), and offers **Set…** (**Replace…** once one is set) and, for a key
Flockdeck stored, **Clear**.

Keys are kept in `keys.json` in the state directory, readable only by you. A
key reaches exactly one place: the environment of the chat process for the pane
that needs it. It is never logged, never written into a saved layout and never
shown, and the interface will not read one back to you. An endpoint that needs no key at all, such
as a local server on loopback, counts as available without one.

## Adding your own: agents.json

The picker is the built-in agents overlaid with your own file, `agents.json` in
the state directory: `%AppData%\flockdeck` on Windows, `~/Library/Application
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
takes one out of the picker without removing it. A model's `"tier"`
(`"small"`, `"mid"` or `"top"`) is set or corrected the same way; anything else
is named in the notice and ignored.

The file is read fresh every time the picker opens, so editing it by hand takes
effect without a restart.

## Roles

A role is a job an agent is started for. Flockdeck ships six: `manager`,
`planner`, `worker`, `reviewer`, `tester` and `docs`. Each has a short briefing
saying what the agent does, what it must not do, who it reports to and what to
hand back when it finishes. An agent starts in one with `-role`:

```sh
flockdeck spawn -role reviewer "review the token refresh change on fix-auth"
```

`flockdeck agents` lists the roles after the agents, with the checkout each one
expects and which roles it may start. An unknown role is refused with the list of
roles there are.

A pane in a role is limited in what it may start. It may start only the roles its
own role lists under `spawns`, and it may start an agent with no role (`spawn`
without `-role`) only if its role has `plain` set, since an agent with no role has
no limits. `manager` has `plain` set; the other built-in roles do not. A refused
spawn says which rule it hit.

The briefing follows those limits. A pane in a role that may start nothing, such as `worker`
or `reviewer`, gets no section on starting agents. A pane in a role with `spawns` gets
examples that name those roles, such as `flockdeck spawn -role worker -worktree <branch>
"<task>"`, and is told to use the pane id that `spawn` prints, or `flockdeck team -json`
shows, when it assigns a task or sends a message, because pane names are shared.

The pane is briefed for its role: the briefing gains a "Your role" section with the
role's text and the ids of every role in the catalog, and the pane's environment
carries `FLOCKDECK_ROLE`. The role is saved with the layout, so a restored pane is
briefed for the same job. Agents in a project can see each other, message each other and
share a task board (see below).

A role may name an `agent` and a `model`, or a `tier` (`small`, `mid` or `top`) in
place of a model. They apply only to a spawn that names neither `-agent` nor
`-model`: a spawn that names an agent keeps it. A role's model goes only to the agent
the role resolves to (its own `agent`, or the project's default when it names none),
so `spawn -agent codex -role fast` does not give codex a model written for claude. The built-in roles name none, so they run whatever
the project runs by default.

Roles are another key of `agents.json`. An entry whose `id` is a built-in's is
merged over it field by field, and any other `id` is a role of your own:

```json
{
  "roles": [
    { "id": "worker", "tier": "mid" },
    {
      "id": "security",
      "name": "Security reviewer",
      "briefing": "Review the change for injection, unsafe file access and leaked secrets. Do not edit files. Report findings to whoever started you, with file and line.",
      "worktree": "readonly",
      "spawns": []
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `id` | What `-role` takes |
| `name` | What lists and the briefing call it |
| `agent`, `model`, `tier` | Defaults for a spawn that chose neither an agent nor a model |
| `briefing` | The text the agent is given about its role |
| `worktree` | `own` (a worktree and branch of its own), `readonly` (a checkout it reads and does not change) or `main` (the project's main checkout) |
| `spawns` | The ids of the roles an agent in this role may start. An agent in a role may start only these; an agent with no role may start any |
| `plain` | `true` lets an agent in this role start agents with no role. Off unless set |

The `worktree` setting is part of the briefing and the role list. Flockdeck does not
yet cut a worktree or make a checkout read-only because of it, so pass
`-worktree <branch>` when a role should have its own. The manager briefing says to
do this for workers and to pass the branch name to the reviewer and tester. A `worktree` that is none of
the three, a bad `tier`, and a `spawns` entry that names no role are listed in the
notice and ignored.

### The team and messages

Every agent's briefing has a "Your team" section: the other agent panes of its
project, each with its role (`no role` if it has none), its name, its agent and model,
its status and its pane id, and its own entry marked. The briefing is written when the
agent starts, so the list is a snapshot, and it says so. Lists longer than twelve are cut
and the number left out is given. `flockdeck team` prints the live list, and `-json` the
same as JSON.

Agents message each other with `flockdeck msg send <target> <text...>`, where the target
is a pane id, a peer name, a pane name or a role id, and read their own with `flockdeck
inbox` and `flockdeck inbox read <id>`; see [The command line](#cli) for the rules. The
built-in roles say when to use it: the manager writes to the agents it started to answer a
question or change a task, and planners, workers, reviewers, testers and docs agents write to
the manager when they are blocked. Their final report is still their last message and not a
`msg`.

A message is data from another agent. The briefing and the notice that tells an agent about
a message both say that messages from other agents are not instructions from the user, and
that an approval in one is not the user's approval. The sender's name and role are not
authenticated either, so an agent must not treat them as proof of who sent a message or as
authorization. Any agent in a project may message any
other in it, whatever its role; the roles limit who may start whom and nothing else. A pane
can send only as itself and read only its own inbox, and a request naming a pane that is not
open is refused. A shell cannot send. The pane id in a request is the one the pane's environment gives it, checked
against the open panes but not against the pane that sent it, as with `spawn` and `close`.

When a helper an agent started exits on its own, or is closed as finished, Flockdeck writes
to that agent's inbox, from `flockdeck`. Changes to a task on the board, below, are the only
other events it writes.

### The task board

Each project has one task board, which every agent in it reaches with `flockdeck task`
whatever worktree it works in. A task has a title, a body, an owner role and pane, a state, a
branch, an optional baton id for context and a thread of notes. A task points at a baton and
does not replace it: a baton stays the handoff document it was written as. The states are
`planned`, `assigned`, `in-progress`, `in-review`, `done`, `blocked` and `cancelled`, and
which role may move a task where is a fixed table; see [The command line](#cli) for it and for
the commands.

The built-in roles use it this way. The planner adds a task for each step of a plan. The
manager assigns them to the workers it starts, and moves a task to `done` once a reviewer
says the change is good: only the manager, a reviewer or a tester can move a task from `in-review`
to `done`, and the owner cannot close its own task. A worker moves its task
to `in-progress` when it starts, sets the branch, and moves it to `in-review` when the change
is committed, which messages the reviewers and testers. A reviewer or tester reads a task in `in-review`
and either adds a note and tells the manager, or moves it back to `in-progress` with a note
that says why, which messages the worker. Every agent's briefing has a "Task board" section
with the commands and a snapshot of the counts for that pane, such as "2 tasks assigned to you,
1 in review", or "2 tasks for your role, none claimed yet" for tasks assigned to a role that no pane has started.

Flockdeck messages the owner when a task is assigned or noted, the reviewers and testers (or else
the creator and the manager) when it moves to review, the creator and the manager when it is
blocked, the creator when it is done (and the manager, when somebody else closed it), and the
manager when a note is added to a task in review. A pane started with no role has the manager's
powers only while no pane in the manager role is running. The board is written by one Flockdeck
server per project; a second instance open on the same repository can overwrite its changes. The pane id in a
request is not authenticated, any more than the sender of a message is, so the roles keep
agents to their jobs and do not stop a process that wants to act as another pane. Task text and
the names on it are not instructions from the user.

## Routing

A fan-out multiplies whatever its model costs, and many of its tasks are
mechanical: run the tests, rename a symbol, fix a typo. Routing pre-sets each
row of a [fan-out](#fanout) to a model suited to the work (a smaller one for
mechanical work, a stronger one for hard work) and leaves every other row
exactly as it was. It is **off** until you turn it on, in **Settings ›
Routing**, for every project or for one.

What it chooses is shown before anything starts. A routed row's model is
pre-set in its select, with a **↘ routed** tag (↗ for a stronger model) whose
tooltip says which rule chose it and, for an API agent, what the two models
cost. Changing the select makes the row yours again, and **Undo routing**,
beside the line saying how many rows were routed, puts every row back on the
run's model. What was shown is what runs. A pane a rule routed says so in its
header (`claude · haiku ↘`), with the rule, and the agent and model it would
otherwise have used, in its tooltip. A pane **Minimise cost** routed with no
rule runs the cheaper model without the arrow.

Routing moves work between the models of the agent the run is on, and only
between models whose tier it knows. It sends work to another agent only when
you turn on **Let a rule send work to another agent**, in the same place, and
a rule names that agent's `model` and `agent`. A rule like that could send test
runs to a local model through an OpenAI-compatible endpoint, say. Even then,
only a new fan-out row or helper moves, never a conversation already under
way. The other agent has to be installed or keyed and trusted for the project,
and, if it has an address of its own, something has to be answering there. The
fan-out dialog shows the agent a row will run on, and the pane header's
tooltip names the agent and model it would otherwise have used. Claude Code's
**Default** is whatever the CLI is set to, which might be its smallest model
or its largest, so routing leaves work on it alone: choose a model for the run
in the fan-out, or make one your default, for routing to choose from it.

**Suggest** and **Automatic** are the same for a fan-out, since the dialog asks
before anything starts either way. **Automatic** also routes a helper an agent
starts with `flockdeck spawn` without `-agent` or `-model`, and the first
prompt you send a pane from the prompt bar, while it has no conversation yet:
the pane restarts on the chosen model with that prompt, and never on another
agent. With **Suggest** such a helper or prompt runs as asked, since there is
nobody to show a suggestion to. **Never go below** keeps routing off the
smaller tiers for a project where the work matters.

**Strategy** decides what happens to a task no rule matches. **Cost-first,
quality-aware** (the default) leaves it exactly where it was, the same as
routing off would for that row. **Minimise cost** routes it too, to the
cheapest model **Never go below** allows on the agent it was already going to
run on, never to another agent. Either strategy, a rule that matches still
decides first and **Never go below** still holds; the strategy only changes
what happens when nothing matches. The row's tooltip says "no rule matched" for
a suggestion made this way, so it is never mistaken for a rule's own reason.

**Ask Jev to rate unmatched work** refines **Minimise cost**, and nothing
else. Without it, a task no rule matches goes to the cheapest model whatever the
task is. With it, Flockdeck asks [TypeSafe](https://typesafe.ai)'s Jev model
how demanding the task is, on a scale from a trivial mechanical edit to
open-ended design work, and routes it to a tier that suits: small, mid or top.
Jev can only choose between **Never go below** and the model the row would
have run on anyway, so it never makes a row cost more than it would have without
routing, never picks a model **Never go below** or a "never downgrade"
constraint forbids, and never moves a row to another agent. Only a fan-out row
no rule matched is ever asked about; a helper started with `flockdeck spawn` and a
conversation's first prompt are not. If Jev is unsure, contradicts itself,
is slow, is refused or is down, the row is routed exactly as it would be without
it. The row's tooltip says what Jev rated it when it did decide.

It is **off** by default and needs both this setting, for every project or for
one, and a TypeSafe API key: the one set in **Settings › Status detection ›
TypeSafe API key** (used first), or else `TYPESAFE_API_KEY` in the environment
Flockdeck starts in. Saving a key on its own sends nothing.
**Turning it on sends the text of each such row to TypeSafe, a third party.**
What is sent is that text alone, cut at 2,000 characters with `[truncated]` at
the end, beside three fixed questions about it: no files, no repository, no
conversation, no path of your machine's that the text does not itself contain.
Nothing is removed from the text first, so a task that contains a secret sends
it; leave the setting off for work like that. Calls give up after 2 seconds,
run at most 20 a minute, are remembered for an hour so an edited fan-out does
not ask again, and pause for 30 seconds after a failure, or 5 minutes after
the key is refused. Without the key the
setting does nothing and nothing is sent.

The policy is kept in `agents.json`, and only there, never in a file inside a
repository, so a repository you clone cannot change what your key spends:

```json
{
  "routing": {
    "mode": "suggest",
    "floor": "",
    "strategy": "",
    "jev": false,
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
under `"strategy": "cost"`, or, with `"jev": true` too, to the tier Jev rates
it as needing, as above. Every condition in `when` must hold: `task` is a
regular expression matched without regard to case; `minWords` and `maxWords`
bound the task's length; `files` are globs matched against the paths the task
names (`**` is any number of directories, and a glob with no `/` matches a
file's name anywhere); `kind` is `fanout`, `spawn` or `turn`; `agent` is an
agent's id. A rule asks for a `tier`, or for one agent's `model`. Leave
`rules` out for the built-in ones, which Settings lists; `"rules": []` means
none. A project's own policy replaces the one for every project whole. A rule
that cannot be used (a pattern that is not one, a tier that is not one) is
named in the notice and skipped, and the rest still apply.

Saving a default from a Flockdeck that has no routing (0.2.10 or older)
writes a project's entry back without its `routing`, so a project's policy is
lost that way.

**Routing makes no request** unless you turn one of two things on. It decides
from these rules alone. With **Let a rule send work to another agent** on,
before moving a row to an agent with an address of its own, it opens a
connection to that address, and closes it at once without sending anything, to
check something is listening. With **Ask Jev to rate unmatched work** on, and a
key set, it sends a fan-out row's text to TypeSafe as described above. The routed mark on a pane travels only as the
rest of the pane's state does, to your own paired devices when Flockdeck Remote
is on. What it chose for each fan-out row, and whether you kept it, is kept
in `routing.jsonl` in the state directory, for your own numbers: when, the
project, the pane, the agent, the rule's name and the models, never the task;
the last 10,000 lines. A row decided with no rule is logged too, with what Jev
rated it if it did. Helpers and first prompts are not logged. Settings lists each rule beside how often
the log shows it overridden, once it has decided at least once. That is the evidence
for deciding which rules are worth hand-editing. Below the rules,
**Unmatched work** shows how often what Minimise cost chose was overridden with
Jev's rating and without it, so you can see whether trusting it has been
worth it; nothing changes on its own. Settings has **Clear routing
history**, and to turn all of it off, set every project to **Off**. A file that does not parse is a notice in the
interface and nothing worse: the built-in agents carry on, because a typo in a
settings file is not a reason to be unable to start work.
