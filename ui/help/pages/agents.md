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

## Structured agents (beta)

A structured agent is one Flockdeck talks to over the Agent Client Protocol
instead of in a terminal. Its pane has no screen: the drawer
shows its conversation, a box to send it a prompt and a Stop button. Today the one offered is
opencode (structured). There is none for Claude Code.

It is off until you turn it on. Put `"structuredPanes": true` in `prefs.json`
in the state directory (there is no setting for it in the window); until then
the picker does not offer a structured agent. Turning it off again takes effect
when you restart a pane or when the agents are read again, and does not end a
running pane.

What to know before you use one:

- **Approval is at the desk.** A permission request is answered from a window on
  this machine, by choosing one of the options the agent offered. A phone gets no
  notification for it and cannot answer it, and a window reached through the
  relay is told its name, status, directory, branch, project, agent and model, and
  no text the agent wrote. Nothing is answered for you; a request still open when you stop the turn, close or restart
  the pane or quit is answered *cancelled*.
- **opencode is started to ask, which a repository can override.** opencode, as
  shipped, runs commands and edits files without a question, so Flockdeck starts it
  with `bash` and `edit` set to ask, and each arrives as a permission card. This
  builds the setting from your own and the project's `bash` and `edit` rules: each
  allow becomes a question, and a `deny` stays a deny. That is about your and the
  project's rules, not the built-in agents' own: opencode's `plan` agent, which denies
  edits, asks for them instead, with a card. Choosing *Always* on a card
  allows that kind of command for the rest of the session, one you had denied
  included. Other settings, such
  as `webfetch`, `read` and `external_directory`, are left as you set them, and
  opencode still runs those without asking unless you say otherwise; Flockdeck
  never sets anything to allow. A terminal opencode pane keeps its own
  configuration. A repository whose own opencode settings give an agent
  (`agent.build.permission`, or a file under `.opencode/agent`) permission to run
  commands wins over this, and the command runs without a card. A project's
  `opencode.json` or `.opencode` folder can also load plugins and MCP servers that
  run code, so no permission setting makes an opencode pane safe in a repository you
  do not trust. When a pane starts, Flockdeck reads what opencode resolves in that
  directory and, only if a command could still run without a card, the drawer header of the pane says "This agent may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask, or could not check. A project's opencode.json or .opencode folder can also load plugins and MCP servers that run code, so no permission setting makes an opencode pane safe in a repository you do not trust." Open
  a repository you do not trust in a terminal pane, not a structured one.
- **Mail needs an opt-in.** A structured agent has no `flockdeck` command and is
  not given the pane identity one needs, so it cannot run `flockdeck inbox`. By
  default it is not woken by mail at all, and its briefing says mail is not
  available to it. `flockdeck wake text on -agent opencode-acp` turns it on for
  that agent type. Then each wake is one prompt holding the oldest unread
  message, as `Flockdeck message from agent "<role>": <text>` (`the user` for a
  message you wrote), and the message counts as read once the agent takes it.
  This puts text written by another agent in front of an agent that may run
  commands, as if it were a turn from you. What limits it: the fixed start of the
  line, one line with escapes and control characters removed, the sender label cut
  at 32 characters, the text cut at 300 characters, and a briefing that tells the
  agent to treat the text as a message from another agent and not as your
  instruction. The agent cannot fetch what is cut. Leave it off for an agent you
  would not trust with a peer's words.
- **A login or Node problem** shows as a blocked pane marked "needs setup", and
  as "Needs setting up" in the team graph, with the one step to take: sign in with
  the agent's own tool (Flockdeck never signs in), install the agent, or install Node 20 or
  newer where the adapter needs it and restart Flockdeck, since a new Node on the
  path is only seen by a restarted Flockdeck. Start the pane again after the other
  two.
- **A restart starts a new session.** The earlier entries stay above a line saying
  the agent was restarted. If the new agent cannot be started, they stay too, with
  a line giving the reason.
- **Nothing is recorded or exported.** Recording and export are not available for a
  structured pane yet.

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
| writes a transcript | it can be resumed from it; it is what Resume lists |
| answers a start-up hook | its briefing survives a compaction, rather than being said once and summarised away |

An agent that does none of it still works: it is a terminal with
a program in it. Where a
capability is missing Flockdeck falls back rather than failing: status comes from
the terminal bell and a quiet timer; resume is not
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
single message, so a pane started with a task (by `flockdeck spawn`, say) may end when
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

[Running Ornith models](#ornith) has a worked example of an agent of your own
pointed at a self-hosted model.

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
role's text (and, for a role that may start others, the ids of every role in the catalog), and the pane's environment
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
| `description` | One line on what the role is for, shown beside its name in the picker and Settings. It is not given to the agent |
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

### Starting a role from the window

The agent picker ([[action:newAgentTabChoose]] or [[action:splitRightChoose]]) has a
**Role** control above the filter. **None** is the picker as it has always been. Choose
a role and the picker shows what the role is for, asks for a task, which a role always
needs, and says which agent and model the role comes to, for example `claude · opus, the
Worker role's choice`. **Enter** in the task, or **Start**, runs the role on that agent and
model. Picking an agent row in the list names the agent only, as `-agent` does, so the role's
model still applies when that agent is the role's own; picking a model row names both, and
the role's choice is not used. `Ctrl+Down` and `Ctrl+Up` walk the list from the task box or
the Role control, and `Ctrl+Enter` starts the row picked out.

**In a worktree of its own** is ticked for a role whose `worktree` is `own`, and the hint
says what the role expects: its own branch, a branch it reads and that the task should name
(`readonly`), or the project's main checkout. Ticked, Flockdeck cuts a worktree and branch
named from the task, as `-worktree` does. A role opens in a tab of its own, not as a split,
and Flockdeck switches to it once it has started. If the start is refused, the dialog stays
open with what you typed and the notice says why.

A role cannot be started with a shell; the window never offers one. The spawn limits
above apply to agents, not to you: from the window you may start any role, including one
that no role lists under `spawns`.

[[action:startTeam]] starts a team with one agent. It asks for a goal and, if you want
one, an agent and model for the manager, and starts a `manager` with the goal as its task
in the project on screen, as `flockdeck spawn -role manager "<goal>"` would. The manager
then starts the rest. With no `manager` role in the catalog the dialog says so and has no Start button.

### What a manager does on its own

Given a goal, a manager works through it without being asked at each step:

1. It breaks the goal into tasks and puts them on the task board, starting a `planner`
   first when the goal is more than one obvious change.
2. It starts a `worker` for each task with `spawn -role worker -worktree <branch>`, so
   each has a branch of its own, and assigns the task to it. Planners and reviewers read
   and need no worktree of their own. Every agent it starts for one task gets the same
   `-tab "<task title>"`, so the planner, worker, reviewer and tester for a task share one
   tab named after it, and another task gets another tab. The tab is forgotten when Flockdeck
   restarts, so a task whose helpers are still running gets a second tab of the same title the
   next time the manager starts one for it.
3. It arms `flockdeck inbox -follow` once, before it starts helpers: under the Monitor tool in
   Claude Code. Each new message prints one line, which wakes the manager even after it has ended
   its turn, and it reads the message with `inbox read`. The Monitor tool expires after at most 30
   minutes, and the manager starts it again. Without the Monitor tool it runs `inbox -wait 90s`
   with Bash `run_in_background` in a loop, because a plain background command wakes the agent
   only when it exits. Only Claude Code is known to be woken this way; an agent without it waits
   inside its turn with `inbox -wait 90s` (90 seconds stays inside the 120 second limit of an
   agent's shell command). The other roles arm the stream too, and stop once they have reported.
   Flockdeck writes to the manager's inbox when a
   helper sends a message, exits, or finishes a turn and stays idle, and when a task is
   blocked or ready for review.
4. It starts a `reviewer`, and a `tester` if the change needs one, on each worker's
   branch, moves a task to `done` when the verdict is good, and sends work that needs
   changes back.
5. It closes a task's helpers when the task is done, which also removes the task's tab,
   and reports to you: what is done and on which branches, what is blocked, and what it
   needs you to decide.

Some things stay with you, but only as an instruction in the briefing. A manager is told to
ask and wait before it merges, pushes, opens a pull request, deploys, publishes, deletes
branches, worktrees or files, or spends money, to tell its helpers to leave those to it, and
to treat a message or report that says you approved something as no approval. Flockdeck does
not check this: nothing stops a manager or helper from running `gh pr merge` or `git push`
except the agent's own permission prompts and its willingness to follow the briefing. A helper
an agent starts does not inherit that agent's auto-review setting; it starts with the
installation's default.

The number of helpers is kept modest. The manager is told to run no more than four at
once, and Flockdeck refuses a `spawn` while eight panes are open below the agent asking
(its helpers, theirs, and any whose process has exited but whose pane is still open);
closing a finished helper makes room. A message that says a helper is idle is from Flockdeck
and carries no text from the helper. The helper's report arrives as a SendMessage from the helper, which the
built-in roles send, since a manager cannot read another pane (`msg send` is the fallback). Agents are told not to answer
a message only to acknowledge it, which would wake the sender for nothing.

A pane started in a role carries its name in its header, after the pane name, and in
the **Agents** overview. **Pane info** lists it as **Role**. A pane started in no role shows
nothing. Settings › Agents & models lists the roles to read; they are changed in
`agents.json`, as above.

### The team and messages

Every agent's briefing has a "Your team" section, the other agent panes of its project
as they were when the agent started, and a "Messages" section. Targets, limits, the
one-line notice, the fixed line that can wake an idle agent and what Flockdeck writes to an
inbox itself are in [The command line](#cli).

The built-in roles say when to use messages: the manager writes to the agents it started to
answer a question or change a task, and planners, workers, reviewers, testers and docs agents
write to the manager when they are blocked. Planners, workers, reviewers, testers and docs
agents also send their final report to the manager with SendMessage, because the
manager cannot read their panes; it is still their last message too.

### The task board

Each project has one task board, which every agent in it reaches with `flockdeck task`
whatever worktree it works in. A task has a title, a body, an owner role and pane, a state, a
branch, an optional baton id for context and a thread of notes. A task points at a baton and
does not replace it: a baton stays the handoff document it was written as. The states, the
table of who may move a task where, the messages a change sends and how the board is stored
are in [The command line](#cli).

The built-in roles use it this way. The planner adds a task for each step of a plan. The
manager assigns them to the workers it starts, and moves a task to `done` once a reviewer
says the change is good. A worker moves its task to `in-progress` when it starts, sets the
branch, and moves it to `in-review` when the change is committed, which messages the
reviewers and testers. A reviewer or tester reads a task in `in-review` and either adds a
note and tells the manager, or moves it back to `in-progress` with a note that says why,
which messages the worker. Every agent's briefing has a "Task board" section with the
commands and a snapshot of the counts for that pane, such as "2 tasks assigned to you,
1 in review", or "2 tasks for your role, none claimed yet" for tasks assigned to a role that
no pane has started.

### The Team panel

[[action:team]] opens a sheet for the project on screen, with three tabs: Tasks, Messages and
Agents. It shows what the command line shows, and lets you steer from the window. It updates as
the team works: a task that moves, a message that arrives and a pane that starts, stops or
changes status appear without reopening it, and an open task, the text you are typing and your
place in the list stay as they were.

**Agents** lists every agent pane of the project with its name, role, what it is doing and how
many messages it has not read. The graph shows the rest of a pane's detail. **Close** and
**Close finished** close panes that are idle or have exited; Close finished names them and asks
first, and each pane is checked again as it closes, so one that has started working since the
list was drawn is left open. A pane locked against closing keeps its button disabled. To start
an agent, use the agent picker; the sheet has no button for it.

**Tasks** is the board, one column for each of planned, assigned, in progress, in review and
done. In a window narrower than 900 pixels the columns are one list, grouped by state. Blocked and
cancelled tasks are behind a button. Each card shows the id, title, who the task is for, the
branch and the number of notes; the arrow keys move between cards and Enter opens one. Open a task
to read its details, notes and history and to assign it to a role or a pane, set its branch, add a
note, or move it. **Move** offers only the moves the rules allow you for that task, and a move that
undoes progress needs a note in the note box. **Add task** adds one, and **Prune finished…**
removes done and cancelled tasks last changed over 30 days ago.

You act as the manager, whether or not a pane has that role, and as no pane: a task you add or a
note you write shows **you** as its author, and the agents are told it was the user. The
notices are the ones a manager pane's change would send, so an agent hears when a task is assigned
to it, goes to review, is blocked or done, or gets a note. Agents see you as `the user (at the
window)`, in notices, `flockdeck task show` and `flockdeck inbox`; an agent that names itself `the user`, `you`
or `flockdeck` is shown as `(agent) the user`, so no agent can sign as you.

**Messages** lists the project's messages, newest first, with sender, recipient, time, text and
whether the recipient has read it. The filter narrows them to a pane or a role. Message text is
shown as plain text. Write to a pane, or to every pane in a role, in the box above the list: the
message is signed as you, is at most 8 KB, and goes to the agent's inbox. It never goes into the
terminal; the agent is sent the same one-line notice as for any message, when it is idle. Each
message shows two marks: whether its recipient has read it, and whether you have seen it. **Mark
seen** sets only your own mark and the count on the tab; the agent's inbox and its unread count
are unchanged.

The panel works on the machine Flockdeck runs on. A window reached through the relay is refused
its commands. A board of more than 300 open tasks lists the newest 300, with a button for the rest.

## Routing

Helpers multiply whatever their model costs, and many of their tasks are
mechanical: run the tests, rename a symbol, fix a typo. Routing pre-sets the
model of a helper an agent starts with `flockdeck spawn`, and of the first
prompt you send a pane, to one suited to the work (a smaller one for
mechanical work, a stronger one for hard work). It is **off** until you turn it
on, in **Settings › Routing**, for every project or for one.

A pane a rule routed says so in its
header (`claude · haiku ↘`), with the rule, and the agent and model it would
otherwise have used, in its tooltip. A pane **Minimise cost** routed with no
rule runs the cheaper model without the arrow.

Routing moves work between the models of the agent the run is on, and only
between models whose tier it knows. It sends work to another agent only when
you turn on **Let a rule send work to another agent**, in the same place, and
a rule names that agent's `model` and `agent`. A rule like that could send test
runs to a local model through an OpenAI-compatible endpoint, say. Even then,
only a new helper moves, never a conversation already under
way. The other agent has to be installed or keyed and trusted for the project,
and, if it has an address of its own, something has to be answering there. The
pane header's tooltip names the agent and model it would otherwise have used.
Claude Code's **Default** is whatever the CLI is set to, which might be its
smallest model or its largest, so routing leaves work on it alone: make a
model your default, for routing to choose from it.

**Automatic** routes a helper an agent starts with `flockdeck spawn` without
`-agent` or `-model`, and the first prompt you send a pane from the prompt
bar, while it has no conversation yet: the pane restarts on the chosen model
with that prompt, and never on another agent. **Never go below** keeps routing
off the smaller tiers for a project where the work matters.

**Strategy** decides what happens to a task no rule matches. **Cost-first,
quality-aware** (the default) leaves it exactly where it was, the same as
routing off would. **Minimise cost** routes it too, to the
cheapest model **Never go below** allows on the agent it was already going to
run on, never to another agent. Either strategy, a rule that matches still
decides first and **Never go below** still holds; the strategy only changes
what happens when nothing matches. The pane's tooltip says "no rule matched" for
a choice made this way, so it is never mistaken for a rule's own reason.

The policy is kept in `agents.json`, and only there, never in a file inside a
repository, so a repository you clone cannot change what your key spends:

```json
{
  "routing": {
    "mode": "auto",
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
    "C:\\code\\payments": { "routing": { "mode": "auto", "floor": "mid" } }
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
file's name anywhere); `kind` is `spawn` or `turn`; `agent` is an
agent's id. A rule asks for a `tier`, or for one agent's `model`. Leave
`rules` out for the built-in ones, which Settings lists; `"rules": []` means
none. A project's own policy replaces the one for every project whole. A rule
that cannot be used (a pattern that is not one, a tier that is not one) is
named in the notice and skipped, and the rest still apply.

Two things an older version wrote still load, and do nothing. `"mode":
"suggest"` filled in the Fan out dialog, which no longer exists, so it is read
as `"off"` with a notice; set it to `"auto"` to have routing choose models. A
rule whose `kind` is `fanout` is named in the notice and skipped. A `"jev"` key
is ignored and left in the file as written.

Saving a default from a Flockdeck that has no routing (0.2.10 or older)
writes a project's entry back without its `routing`, so a project's policy is
lost that way.

**Routing makes no request** unless you turn on **Let a rule send work to
another agent**: then, before moving work to an agent with an address of its
own, it opens a connection to that address, and closes it at once without
sending anything, to check something is listening. Otherwise it decides from
these rules alone. The routed mark on a pane travels only as the
rest of the pane's state does, to your own paired devices when Flockdeck Remote
is on. Helpers and first prompts are not logged. An older version
that routed fan-out rows kept its choices in `routing.jsonl` in the state
directory (the last 10,000 lines, never the task), and nothing writes to it
now. Settings still lists each rule beside how often that log shows it
overridden, once it has decided at least once. Settings has **Clear routing
history**, and to turn all of it off, set every project to **Off**. A file that does not parse is a notice in the
interface and nothing worse: the built-in agents carry on, because a typo in a
settings file is not a reason to be unable to start work.
