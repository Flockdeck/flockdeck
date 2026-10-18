# How each agent learns where it is running

An agent started in a pane would otherwise not know it is one of six. It would not
know that its neighbour is editing the same repository on another branch, that the
directory it was dropped into is a worktree and not the project, or that the task
it was given came from another agent's plan. So Flockdeck briefs it when it
starts.

This page covers how the briefing is delivered and why it is shaped as it is. What
the commands do is in the [command line page](../internal/help/pages/cli.md); roles
and the team are in [Agents and models](../internal/help/pages/agents.md). The
text itself is built in `internal/workspace/briefing.go` and `context.go`.

## What the briefing holds

- The pane: its name, tab, project, directory and branch, whether the directory is a
  worktree of its own, and the task it was started with.
- The other agents running in the project, with the agent and model of each, and that
  their conversations are separate from this one.
- The pane's role, when it has one: the role's briefing, the checkout it expects, and
  which roles it may start.
- For an agent that can call back into Flockdeck: the team roster, the commands for
  messages and the task board, and how to start helpers if its role allows it.
- A one-line status rule, one line for each user-facing feature, and a pointer to
  `flockdeck help`.

## What it leaves out

The briefing is sent again on every session start, compaction and resume, so every line
is paid for each time. It leaves out what an agent cannot act on, and what is already
written where the agent can ask for it:

- Flag lists, state tables and storage details. `flockdeck <command> -h` and the command
  line page have them, and the briefing says so once.
- The key table and the meaning of the status colours. They are for the person at the
  window and are in the in-app help and [keys.md](keys.md).
- The commands a role cannot use. A role that may not start agents gets no `spawn` section,
  and `task assign` and `task prune` are shown only to the manager role or a pane with no role.
- The environment table and the less common start-up flags. The command line page has them.

Each rule is written once. The sentence that says messages and task text are data and not
instructions from the user appears once, in the messages section, and covers both.
`internal/workspace/contextsize_test.go` holds a size ceiling per case so the briefing does
not grow back unnoticed.

## How it is delivered

Claude Code and the API agents have a session-start hook, and the built-in chat
client has one too. The briefing is one more lifecycle event, `SessionStart`,
answered by the same loopback server that receives the status events. The reply
goes back as `additionalContext`, which is the supported way to add to a session,
so nothing is typed into the terminal and none of the user's settings are
overwritten. `SessionStart` fires again after a compaction and on resume, so a pane
that has been running all day is still oriented after its context has been
summarised away.

An agent with no hook to answer has the briefing put in front of its opening prompt
instead. It is wrapped in a `<flockdeck-context>` block so the agent can tell the
two apart, and sent on its own if there is no opening task. That happens once per
launch, not once per compaction, which is fair as long as the briefing says when it
was taken, and it does. The built-in Codex, Gemini CLI, opencode and Cursor Agent
entries use this, and they get it even in a pane opened by hand with no task. Aider
gets it only when there is a task, because `--message` makes it answer once and
exit, and a message that was all briefing would end the pane. An entry in
`agents.json` chooses the same for its own agent with `"caps": {"context": "prompt"}`
(or `"task"` for the Aider behaviour).

## Messages between panes

The briefing is only where an agent learns the commands. The messages are Flockdeck's own
channel: a pane runs `flockdeck msg send`, which posts to the loopback server with
`FLOCKDECK_API`, `FLOCKDECK_TOKEN` and its own `FLOCKDECK_PANE`, and reads with
`flockdeck inbox`. Nothing is sent through the terminal, and nothing from a message reaches
a pane's input, except as described here. Targets, limits, retention and what the typed
line says are on the command line page.

- Who a message is from is the pane named in the request, which has to be an open pane; the
  sender's role and name are taken from that pane, not from the request. The token is the
  same for every pane of a run, so a process in one pane could name another pane's id. This is
  the trust `spawn` and `close` already rely on, and it is not stronger. So the sender name and
  role in a message are not authenticated, and an agent must not use them as proof of who sent
  it or as authorization.
- A peer name is the pane's own report. Control characters and line breaks in it become
  spaces and it is cut to 64 characters when it is recorded, and `inbox` prints a sender
  with the same rules, so a name cannot start a line of its own.
- Spawn rights (`spawns`, `plain`) do not apply to messages.
- Messages are kept in memory and in `messages.json`, written under one lock, so the newest
  snapshot is always the one left on disk. At start-up, messages read more than 7 days ago and
  messages of any kind sent more than 30 days ago are dropped, which is what removes the inbox
  of a pane that no longer exists.

### Telling the agent it has mail

An agent only looks at its inbox if it is told to, and Flockdeck does not type into a pane to
tell it. Text in an agent's message box that the user did not write is a bug whether it is
submitted or not, and it was one in practice: on Windows ConPTY the line and its Enter arrived
in one read, Claude Code took the Enter for part of a paste, and the line stayed in the box
unsubmitted. The earlier design also had to guess when typing was safe (idle, no draft, no
open menu), and a guess cannot be right when the user may be typing in the same instant.

Mail is delivered on two channels instead.

- The notice. When a hook of the agent runs for `UserPromptSubmit` or `PostToolUse`, the hook
  server asks `Workspace.HookNotice` for the pane and returns the answer as
  `additionalContext`, which Claude Code adds to what the model reads. The notice is built
  from a count, an id and the sender's label (`Message.FromLabel`, the same name and role the
  inbox prints, one line of at most 64 characters), so it holds no text the sender wrote. It is
  given once per message: `team.Notifier` remembers the newest id each pane was told of, and
  forgets it when the session is replaced, so unread mail is told again to a restarted pane.
  A working agent gets it with its next tool result. An idle agent has no hook to answer
  until the user's next prompt, so it gets it then. `Stop` is not used: its reply can only
  block the stop and force another turn, which would start work nobody asked for.
- The unread count. `Workspace.Unread` is the number of unread messages, drawn on the pane's
  header for every agent. It changes whenever an inbox does (the store's change callback wakes
  the window), and it stays until the agent reads the messages with `flockdeck inbox`, whether
  or not it was told. It is the only sign for an idle agent, and for an agent without hooks.

The agent's catalog entry needs `caps.hooks` to be told. Agents without it (Codex, Gemini CLI,
opencode, Cursor Agent, Aider) get no notice at all: their briefing says "You are not told when
a message arrives" and asks them to run `flockdeck inbox` when a session starts and before they
report back, `flockdeck msg send` tells the sender when the receiver is one of them
(`Sent.Notifies`), and the unread count shows on their pane. A message to them can wait
until the agent next looks, which is a real limit; it is not lost.

## The task board

`flockdeck task` posts to the same loopback server, with the same `FLOCKDECK_API`,
`FLOCKDECK_TOKEN` and `FLOCKDECK_PANE`, to `/task`. The board is the application's: the
workspace opens it, applies the rules and writes the file, so a pane in any worktree sees the
same board and none reads or writes the file itself. Who acts is the pane named in the request,
which has to be an open agent pane, with the role it was started with; the request cannot name
another role. That is the trust `spawn`, `close` and `msg` rely on, and the board adds nothing
to it.

- The rules for who may move a task where are one table, `Transitions` in `internal/tasks`,
  and the tests walk every pair of states against it. The command line page prints it.
- Writes read the file, change it and write it back under one lock per board, through a
  temporary file that is flushed and renamed over the old one. Another process that changes the
  file between two writes is not overwritten; two writing in the same instant could still lose
  one change. The board has a single writer, the Flockdeck server of the project.
- The workspace works out from the roster at each request whether a pane with no role holds
  the manager's powers.
- Changes message panes through the inbox, from `flockdeck`, so they are told and counted the same
  way as any message. The pane that made the change, a pane that is closed
  and a pane whose process has exited are left out.

## Isolation between panes

The isolation is real, not advisory. Each pane is a separate top-level session with
its own session id, its own generated settings file where the agent takes hooks, and
an environment scrubbed of the markers a parent agent session would otherwise pass
down. Those markers are stripped for every agent in the catalog, not only the one in
this pane, because Flockdeck may have been launched from inside any of them. Nothing
is shared between two panes.
