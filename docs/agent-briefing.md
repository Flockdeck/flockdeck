# How each agent learns where it is running

An agent started in a pane would otherwise not know it is one of six. It would not
know that its neighbour is editing the same repository on another branch, that the
directory it was dropped into is a worktree and not the project, or that the task
it was given came from another agent's plan. So Flockdeck briefs it when it
starts.

## What the briefing says

- Which pane it is, in which tab and which project.
- The directory and branch it has, and whether that is a worktree of its own and not
  the project root.
- What it was spawned to do, when a fan-out or another agent started it and a person
  did not.
- Which other agents are running beside it: where each is working, which agent and
  model each is (`codex · gpt-5.6-sol`, because what is in the next pane changes
  what is worth asking of it), and what each was asked for. It also says that the
  conversations are separate, so nothing passes between panes except through the
  user or a commit.
- That it can start agents of its own with `flockdeck spawn`.
- What the application around it can do (the status the user is watching, the
  fan-out that reads its own output, broadcast, the diff, the worktree panel and what
  a restart keeps), with the keys for each. The keys come from the same table the
  command palette and the help pages are drawn from, so an agent can answer a user
  who asks how to do something.
- What its pane carries in its environment, and what the rest of the command line
  does, including that `-quit` stops every agent in every project and not only this
  pane.

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

## The environment

Every pane also carries `FLOCKDECK_PANE`, `FLOCKDECK_PANE_NAME`,
`FLOCKDECK_PROJECT`, `FLOCKDECK_AGENT`, `FLOCKDECK_MODEL` and `FLOCKDECK_LAUNCH`.
The [command line page](../internal/help/pages/cli.md) lists what each one is for,
and which of them are also set under their older `PERCH_*` names.

## Isolation between panes

The isolation is real, not advisory. Each pane is a separate top-level session with
its own session id, its own generated settings file where the agent takes hooks, and
an environment scrubbed of the markers a parent agent session would otherwise pass
down. Those markers are stripped for every agent in the catalog, not only the one in
this pane, because Flockdeck may have been launched from inside any of them. Nothing
is shared between two panes.
