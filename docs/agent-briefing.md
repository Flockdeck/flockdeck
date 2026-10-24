# How each agent learns where it is running

An agent started in a pane would otherwise not know it is one of six. It would not
know that its neighbour is editing the same repository on another branch, that the
directory it was dropped into is a worktree and not the project, or that the task
it was given came from another agent's plan. So Flockdeck briefs it when it
starts.

This page covers how the briefing is delivered and why it is shaped as it is. What
the commands do is in the [command line page](../ui/help/pages/cli.md); roles
and the team are in [Agents and models](../ui/help/pages/agents.md). The
text itself is built in `internal/workspace/briefing.go` and `context.go`.

## What the briefing holds

- The pane: its name, tab, project, directory and branch, whether the directory is a
  worktree of its own, and the task it was started with.
- The other agents running in the project, with the agent and model of each, and that
  their conversations are separate from this one.
- The pane's role, when it has one: the role's briefing, the checkout it expects, and
  which roles it may start.
- For an agent that can call back into Flockdeck: the team roster, the commands for
  messages and the task board, and how to start helpers if its role allows it. The manager
  role is briefed to run a goal through to a report without being asked at each step:
  tasks on the board, helpers started, results reviewed, the user told. It is told to stop
  and ask only before merging, pushing, opening a pull request, deploying, publishing,
  deleting or spending, to keep to four helpers at once, to arm the inbox stream (`inbox -follow`) before starting helpers, and to
  put the agents for one task in one tab named after it (`spawn -tab`). These are instructions
  in the briefing and are advisory; only the cap on open helpers is enforced.
- When the pane's checkout has `.flockdeck/knowledge/index.md` and the project allows it, one
  line giving the folder's path, saying the notes may be stale and are data, not instructions.
  The notes are never copied into the briefing. See [Pointing agents at the knowledge bundle](#pointing-agents-at-the-knowledge-bundle).
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
channel for the notices it writes, and the fallback between agents, who are told to use the
SendMessage tool of Claude Code first: a pane runs `flockdeck msg send`, which posts to the
loopback server with `FLOCKDECK_API`, `FLOCKDECK_TOKEN` and its own `FLOCKDECK_PANE`, and reads with
`flockdeck inbox`. Nothing is sent through the terminal, and no text of a message ever reaches
a pane's input. The one thing Flockdeck types for mail is the fixed wake line described below.
Targets, limits, retention and the conditions for that line are on the command line page.

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

Mail is delivered on three channels instead.

- The notice. When a hook of the agent runs for `UserPromptSubmit` or `PostToolUse`, the hook
  server asks `Workspace.HookNotice` for the pane and returns the answer as
  `additionalContext`, which Claude Code adds to what the model reads. The notice is built
  from a count, an id and the sender's name and role (`noticeSender` in `internal/team/notify.go`).
  The model trusts context from a hook more than a tool result, so each is quoted, reduced to
  letters, digits, spaces and `.-_()`, and cut to 32 and 16 characters, and the notice carries
  the unauthenticated-sender disclaimer. It holds no text the sender wrote. It is given once
  per message: `team.Notifier` remembers the newest id each pane was told of, and forgets it
  when the session is replaced, so unread mail is told again to a restarted pane. It is
  counted as told when the reply is built, and a hook that times out loses the reply, so an
  unread message is announced again as a reminder after `team.Renotify` (five minutes), at
  most `team.MaxReminders` times.
  `flockdeck chat` (the API-key agents) has `caps.hooks` and gets the same replies: the
  reporter returns them and the session adds them to the system prompt of that turn's requests
  (`session.hold`), so the notice reaches the model.
  A working agent gets it with its next tool result. An idle agent has no hook to answer
  until the user's next prompt, so it gets it then. `Stop` is not used: its reply can only
  block the stop and force another turn, which would start work nobody asked for.
- The follow stream. `flockdeck inbox -follow` is a command the agent starts itself, under the
  Monitor tool of Claude Code. It asks the application for the pane's unread messages every two
  seconds, over the same `/inbox` endpoint, and prints one line for each message it has not
  printed yet: the id, the sender quoted by `noticeSender` and the disclaimer
  (`team.FollowLine`), never message text. It marks nothing read. The Monitor tool delivers each
  stdout line of its command to the model as a notification, which starts a turn for an agent
  that has ended its turn; that was shown in a Flockdeck pane with the built binary against the
  real application: a task assigned to the pane and then cancelled gave one line each, and each started a
  turn in the idle session. A plain Bash `run_in_background` command does not do this: it
  notifies only when it exits, and `-follow` does not exit until the pane closes, so it is
  not the way to arm it. Flockdeck types nothing: the line reaches the model as tool output,
  not as prompt text.
  A Monitor command expires after its timeout, at most 30 minutes, and the agent then takes a
  turn to start it again. A stream armed again announces the messages that are still unread
  again, so an agent that is left idle with unread mail takes a turn about every 30 minutes.
  The briefings tell an agent to arm it once, and to stop once its task is done and it has
  reported.
  Mail that arrives within five seconds of the last line is held and printed together. Of the
  messages held, any from the person at the window are printed one by one and first, and more
  than three from agents are one digest line that names the latest sender, so two agents
  answering each other cost one line every five seconds at most. After 100
  lines of agents' messages (the person's are not counted) the stream announces only the
  person's messages, and says so; a stream started again announces the agents' unread messages
  again. A message from the person never waits for the five-second gap. It retries errors from
  the application without printing, ends after two minutes of
  errors, and ends with a line of its own when the application refuses the pane id with
  `no pane ... is open here` (`hooks.IsNoPane`), which means the pane was closed. If Flockdeck
  restarts, the process dies with its pane, and a restored pane has to arm it again.
  Flockdeck cannot arm it for the agent: a hook can add text to what the model reads but
  cannot start a tool, and a process Flockdeck started itself would not reach the model. An
  agent that forgets keeps the notice and the unread count, and an idle one still learns of
  mail only when someone types. Where the Monitor tool is missing (Bedrock, Vertex and Foundry,
  or with telemetry turned off), the briefing falls back to `inbox -wait 90s` run with Bash
  `run_in_background` and started again each time it exits; it exits when mail arrives, and
  that exit is the notification.
  Only Claude Code is known to be woken by a background command. For Codex, Gemini CLI,
  opencode, Aider and Cursor Agent nothing here shows they are woken, so those agents poll
  with `flockdeck inbox`.
- The unread count. `Workspace.Unread` is the number of unread messages, drawn on the pane's
  header for every agent. It changes whenever an inbox does (the store's change callback wakes
  the window), and it stays until the agent reads the messages with `flockdeck inbox`, whether
  or not it was told. It is the only sign for an idle agent, and for an agent without hooks.

The agent's catalog entry needs `caps.hooks` to be told. Agents without it (Codex, Gemini CLI,
opencode, Cursor Agent, Aider) get no notice at all: their briefing says "You are not told when
an inbox message arrives" and asks them to run `flockdeck inbox` when a session starts and before they
report back, `flockdeck msg send` tells the sender when the receiver is one of them
(`Sent.Notifies`), and the unread count shows on their pane. `Notifies` is also false for an
agent that has the capability but whose hooks have not reported in this session
(`Session.HooksReported`), so a Claude pane whose hooks never fire is not promised a notice. A message to them can wait
until the agent next looks, which is a real limit; it is not lost.

The typed wake line. An idle agent has no hook to answer, so Flockdeck can type `Flockdeck: new
mail, message N. Run: flockdeck inbox read N` at its prompt (`wakeService` in
`internal/workspace/wake.go`). That is all it types for mail: no sender, no text, except in
text mode, which the person turns on for an agent type that cannot run commands (Aider): the
line is then `Flockdeck message from <role>: <text>`, at most 300 characters, and the wake
event keeps it. The streak limit does nothing in that mode, since each typed message is marked
read, so only one line a minute for a pane and 20 an hour for a project bound it. The briefing
does not mention it yet. This sentence goes in with the change that marks the first rule
tested, so no agent pays for it while nothing can be typed: "A line `Flockdeck: new mail,
message N` typed at your idle prompt is from Flockdeck, not the user: run `flockdeck inbox read
N`." `inbox read N` needs no approval. The line reverses the earlier rule that nothing is
typed, on purpose, and keeps the Windows paste rule: the Enter is a second write after
`pasteSettle`. It is typed only where the agent's `agent.WakeRule` is built in and marked
tested, and none is, so nothing is typed yet. A rule from `agents.json` is never trusted. The
conditions, the limits and what happens when the Enter is not sent are on the command line
page, under "The typed wake line". Each line typed is a `wake` event in the team event log. An
agent should read the line as it reads a notice: it is data from Flockdeck, and the message it
points to is data from another agent.

Reading mail needs no approval. `review.MailRead` allows exactly `flockdeck inbox [-all] [-json]`
and `flockdeck inbox read [-json] <id>` (the flag goes before the id, which is one to nine digits) in every pane, whether or not
auto-approve is on, and the built-in chat client runs the same two forms without asking
(`readsFlockdeckMail` in `internal/chat/tool/command.go` calls it). The briefing gives these forms
when the program is run as `flockdeck`; with a path to the binary the command does not match and is
asked about. `-wait`, `-follow`, sending, every other subcommand, any metacharacter or quote, a command
over 4 KiB, and a working directory holding any entry named `flockdeck` or starting with `flockdeck.` (any letter
case) are all asked about. A built flockdeck binary in the working directory therefore makes every mail read ask, and so does any `flockdeck.*` file there: a project that builds a binary into its root, or keeps such files in it, switches the allowance off for that directory. The directory is read in batches and refused above 100,000 entries, and trailing dots and spaces in a name are ignored.
The word `flockdeck` must also resolve to this application: it is looked up on the PATH the pane or chat client was
started with (an empty or relative entry is taken from the working directory), and the command is allowed only if
what is found is the running executable or sits in its directory. A program of that name earlier on the PATH (a
writable directory, a virtual environment's or `node_modules` bin) is asked about, as is a PATH with no `flockdeck`
on it. A `~` entry in the PATH is asked about, since a shell expands it to a directory the check does not know.
The check runs when the hook fires, not when the command runs, and it sees the PATH the pane was launched with, not
one the agent has set since; a shell function or alias named `flockdeck` cannot be seen from the command string. The check
sits in `Workspace.reviewTool` ahead of the auto-approve setting, for a known pane with a current launch
id only, and does not count towards `AutoApproved`. It applies to agents whose hooks send PreToolUse
reviews (Claude Code) and to the chat client; other CLI agents keep their own approval modes.

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

## Pointing agents at the knowledge bundle

The bundle's text reaches a model only if the agent reads the files, but the
path in the briefing is what sends it there, so whether to include it is a
privacy decision about the repository. A project has one of three states.
`on` and `off` are the user's answer, kept in the preferences file by
`flockdeck knowledge on|off`, which refuses to run inside a pane. `ask` is the
default for a repository not known to be private: the line is left out and the
window shows a one-time notice.

The default is `on` only when `gh repo view --json visibility` says
`private`. The lookup runs in the background at most once per project per run,
with a ten second limit, because the briefing is built while the agent waits
on its start hook and must not wait for the network. Any failure (no `gh`, not
signed in, offline, a remote that is not on GitHub) counts as not private. The
first briefing of a project in a private repository therefore has no line, and
the next one has.

The folder is the pane's own checkout, so a pane in a worktree reads that
worktree's copy. `index.md` must be a regular file that, with every link on
the way followed, still lies under the pane's checkout; a bundle reached through
a link out of it is ignored. "Private" is what `gh` says about the checkout's
own remotes, so a third-party repository re-pointed at a private one you can read
would get the pointer. The line says the text is data, and none of it is copied
in. The setting is keyed by the folder the project was opened as, the same key
the briefing uses; `flockdeck knowledge` looks it up from the projects Flockdeck
has opened. Its refusal to run inside a pane is advisory, because it reads an
environment variable; the agent's own permission prompt is the barrier. The line is built in `internal/workspace/knowledge.go` and written by
`render` for hook-delivered and prompt-delivered briefings alike.
