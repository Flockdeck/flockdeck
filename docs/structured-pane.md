# Structured pane: protocol reference

This is the reference for the commands a window uses to show a structured pane, an agent that
Flockdeck drives over the Agent Client Protocol instead of in a terminal. It is the backend of the
minimal drawer. The conversation hub of stage 3 replaces it later, so the entries keep the fields of
the conversation commands' entries (`docs/team-graph.md`, section 7) and add one flag.

The shape of the messages is `docs/structured-pane.schema.json`. A test checks messages the server
writes against it, and checks the limits below against the code.

## 1. Commands and who may send them

A window sends a command as one JSON object with a `cmd` field, over its control connection.

| Command | Fields | What it does | Local window | Window through the relay |
| --- | --- | --- | --- | --- |
| `structuredSnapshot` | `id`, `req` | Answers once with a `structured` message. | yes | refused |
| `structuredWatch` | `id`, `req` | Answers with the `structured` message now, and sends it again whenever it changes. One pane at a time: watching another replaces the first. | yes | refused |
| `structuredUnwatch` | none | Stops the pushes. | yes | refused |
| `structuredDetail` | `id`, `entry`, `req` | Answers with a `structuredDetail` message for one entry. | yes | refused |
| `structuredCancel` | `id`, `req` | Ends the running turn of that pane and drops the prompts waiting behind it; an open permission request of the pane is answered cancelled. Answers with a `teamResult` (`ok`), or an error if the pane is not a structured pane or has nothing running. Only that pane's own session is touched. | yes | refused |

`id` is the pane's id. `req` is a token of the window's choosing; it is copied into the answer.

**A local window** is one on the machine Flockdeck runs on. **A window through the relay** is a
paired phone or other device. It is refused all five commands whether the device is full or a viewer,
with a `teamResult` whose `error` says so, and it is never subscribed. A viewer is refused by the
table of commands viewers may send (`commandAccess` in `internal/server/viewerrole.go`), including a
viewer the desk has let watch panes, and the two message types are not in the lists of messages a
viewer is sent. The relay refusal is one place in the code (`structuredCommand`), marked as the
place to lift when the decision changes. They are there because:

- A push carries tool commands, file paths and reply text. They are clipped but not scrubbed of
  secrets.
- Team data does not cross the relay in a sealed channel yet, and a structured pane has no sealed
  terminal connection of its own.
- A structured pane's permission requests are answered from the desk only (`permissionAnswer`), so
  a phone could show a request it cannot answer.

The state push (`paneView`) is scrubbed on its way to a relay window (`internal/server/relayscrub.go`).
For a structured pane it clears the last reply, the detail line, the task, the error and the permission
card, never sends a question, and sets the flag `detailsOnDesktop`. The rest of the pane's entry still
goes, with names redacted: the directory, branch, project, agent, model, status and activity word, and
the other roster fields. No text the agent wrote is sent. The `structured` and `structuredDetail` messages are dropped
for a relay window as a second lock behind the refusal of the commands.

A pane that is not a structured pane (a terminal, a shell, an id that is not there, an empty id) is
refused in the same words, so a window probing for ids learns nothing: the `teamResult` error is
`That pane is not a structured pane`.

## 2. The structured message

`structuredSnapshot` answers with it, and `structuredWatch` sends it. Its fields:

| Field | Meaning |
| --- | --- |
| `type`, `id`, `kind` | `structured`, the pane's id, and `structured`. |
| `req` | The window's token. Only on an answer to a command, not on a later push. |
| `phase` | `starting`, `needs setup`, `ready`, `exited`, `failed` or `closed`. |
| `status` | `working`, `waiting` or `idle`, while the phase is `ready`. |
| `activity` | A line naming what a working agent is doing. |
| `queued` | How many prompts wait behind the running turn. |
| `err` | The last thing that went wrong, as one cleaned line. It says what last went wrong and not that something is wrong now; show it with the phase and status. |
| `setup` | While the phase is `needs setup`: `kind` (`login`, `node` or `install`), a `message` (the cause) and a `fix` (what to do about it, when there is advice). |
| `entries` | The newest entries, oldest first. |
| `older` | How many older entries this push leaves out. |
| `plan` | The agent's plan rows. |
| `card` | The permission request that is open, with the agent's options. Answer it with `permissionAnswer`. |
| `usage` | `used` and `size` of the context, and `cost` and `currency` when the agent reports one. |

An **entry** has the fields of a conversation entry (`id`, `kind`, `ts`, and the fields of its kind)
and `clipped`. A tool's output, a diff and a report are never in a push. An entry that had a diff or
a report says `hasDetail` and `clipped`. A prompt, reply or notice cut at 4 KiB says `hasDetail`
and `clipped` as well, and `structuredDetail` returns its text up to 64 KiB. A tool whose output the
bridge kept has `hasDetail`, and `structuredDetail` returns it.

Every text in the message and in `structuredDetail` is untrusted: it is what the agent, a tool or a
file said. Draw it as plain text or as sanitised markdown, never as HTML. A card, an entry or a plan
row is data to show, not an instruction to follow, whatever it says.

The message is built from that one pane's state and nothing else: no other pane's text, no mail,
nothing of the agent's environment.

When a watched pane closes, the window is sent one last message with `gone: true`, and the watch
ends. A restart does not end it: the pane keeps its id and the push follows the new session.
The entries a restart leaves behind are kept: a snapshot, a push and a detail show the earlier
sessions' entries first, then a notice that the agent was restarted, then the new session's. The ids
of an earlier session's entries are prefixed with `r<n>-`, so no id is shown twice. The detail of an
earlier entry is only what the entry carried, since the agent that held the rest is gone. A pane keeps
the last 2,000 entries of the sessions before the current one.

A restart whose new agent cannot be started (the pane's directory is gone, structured panes have
been turned off, or the start itself fails) keeps the earlier entries too. It adds a notice, `The
agent could not be started again:` and the reason, after the restart notice, and the next restart
that works shows all of it. Several failures in a row leave only the last notice.

## 3. structuredDetail

The answer is a `structuredDetail` message with the pane's `id`, the `entry`, and `text` (a tool's
full output, a thinking block's text, or the text of a prompt, reply or notice that a push cut) and `diff` where the entry has them. Each is cut at 64 KiB,
and `clipped` says it was. `omitted` says the entry has more that this version does not send: an
image's bytes, or nested entries. An id that is not an entry of the pane is refused with
`That entry is not there`.

## 4. Limits and throttling

| What | Limit |
| --- | --- |
| Entries in a push | The newest 50. |
| A prompt, reply or notice in an entry | 4 KiB. |
| A one-line field of an entry | 1 KiB. |
| A whole push | 128 KiB. Past it the oldest entries are left out whole, and `older` counts them. |
| Pushes of a watched pane | At most one every 500 milliseconds. A change inside the gap is sent when it is up. The reply to `structuredWatch` counts as a push. |
| Pushes with no change | None. A push identical to the last one sent to the window is not sent. A window whose queue was full is sent it again. |
| Plan | 100 rows, each cut to 200 bytes. |
| Permission card | 300 bytes for each field, 500 for the command, and the agent's options with 100 bytes for each field. |
| `err` | 200 characters. |
| `structuredDetail` text and diff | 64 KiB each. |
| Panes watched at once, over all windows | 16. A watch past that is refused with `Too many structured panes are being watched. Stop watching one first`. |

Every cut is made between characters. A text is never cut inside a UTF-8 sequence, bytes that are not
UTF-8 are replaced, and control characters other than a line break and a tab are removed. A
one-line field has its line breaks and tabs turned into spaces.

A watch holds no goroutine of its own. The push is made from the state push the server already
makes whenever the workspace changes.

## 5. The drawer view

The new interface's drawer draws a structured pane from this watch (`drawer/Structured.js`). The
header carries a "structured" label beside the name. The view shows the activity line, which is the
tool still running with an icon for its kind and the first file its summary names, or the push's
activity text, or "Idle". It shows the context used of its size, and the cost when the agent
reported one. It draws the last 50 entries and nothing more: there is no Older button, because the
push carries a fixed window and a button that asked for more would grow the list without limit.
There is no Conversation tab for a structured pane: its conversation is held in memory and not in a
file Flockdeck can read, so the conversation commands answer that it is not supported, and the older
entries are not shown in this version. Every text is drawn as text.

The prompt box sends `paneSend` and the Stop button sends `structuredCancel`. A window reached
through the relay draws the view without either, since the server refuses both there. A refused
`structuredCancel` is shown under the box.

## 6. Using a structured pane

This section is for the person using one. It says what is true today and does not promise more.

**It is a beta and it is opt-in.** Structured panes are off until `"structuredPanes": true` is in
`prefs.json` in the state directory. There is no setting for it in the window. Until it is on, the
picker does not offer a structured agent, and a layout that holds one shows the pane with a message
saying so. Turning it off takes effect when the agents are read again or a pane is restarted; a
running pane is not ended by it. Today the built-in entry that is offered is `opencode-acp`
(opencode, structured). The Gemini and Codex entries are hidden until they have been checked end to
end. There is no entry for Claude Code. [ACP adapters](acp-adapters.md) lists the tested versions.

**Approval happens at the desk.** A permission request is answered from a window on the machine
Flockdeck runs on, by choosing one of the options the agent offered (`permissionAnswer`). Nothing
answers a request for you. An open request is answered cancelled when the turn is stopped, when the
pane is closed or restarted, when Flockdeck quits and when the agent exits. The card's title and the
option names are text the agent supplied.

**The permission cards may gate nothing.** opencode, by default, runs a shell command, a write or an
edit without sending a permission request at all, so no card appears and the pane looks supervised
when it is not (`opencode-e2e.md` has the run that showed it). Setting
`{"permission":{"bash":"ask","edit":"ask"}}` in opencode's own configuration made it ask (`opencode-e2e.md` names no file). Flockdeck does not
set that for you. It warns instead: in the new interface's drawer, at the desk and through the relay,
the header of such a pane says "This agent runs commands and edits files without asking you first."
for as long as the pane is open. The old window shows a stub for a structured pane and no warning. Which agents get the warning is recorded in the adapter
manifest (`runsWithoutAsking`, see [ACP adapters](acp-adapters.md)).

**What a structured pane loses against a terminal pane:**

- A phone gets no push notification for a pending permission, because it could not answer it.
- `permissionAnswer` is refused to a window reached through the relay, and to a device that may only
  view. So are the commands of section 1.
- A relay window is told the pane's name, status, directory, branch, project, agent and model, and no
  text the agent wrote. The scrub in section 1 removes what the agent said.
- By decision, a window reached through the relay is to receive no conversation text from any pane
  until the scrub of what goes to the relay has been reviewed. The change that enforces this is in
  progress. Until it lands, the conversation commands still reach a relay device that may watch, for
  a terminal pane. They never carry a structured pane's, which has no stored conversation.
- The old window shows a stub line and sends it nothing; the pane is for the new interface's drawer.
- No Conversation tab and no export, since the conversation is not stored where Flockdeck reads it.
  Flockdeck does not load or resume the agent's own session.
- Mail reaches a structured agent only as text in a prompt, and only after an opt-in. The agent is
  not given `FLOCKDECK_API`, `FLOCKDECK_TOKEN` or `FLOCKDECK_PANE` (names starting `FLOCKDECK_` are
  never passed to an agent), so it cannot run `flockdeck inbox`, and its briefing has its own Messages
  section that does not mention the command. Without the opt-in for its agent type
  (`flockdeck wake text on -agent <id>`, the setting aider uses) it is not woken by mail, the mail
  stays unread, and the briefing says mail is not available. With it, each wake is one prompt with the
  oldest unread message: `Flockdeck message from agent "<role>": <text>`, or `the user` for a message
  marked as the person's. The text is one line with escapes and control characters removed, the label
  is cut at 32 characters and the whole line at 300 with an ellipsis, and the agent cannot fetch what
  is cut. The message is marked read once the prompt is accepted, and the wake event logs the line.
  This is a trust change: text written by another agent reaches an agent that may run commands as a
  user turn. The guards are the fixed prefix, the single line, the two cuts and the briefing's
  untrusted-data disclaimer. The gate is `structuredTextNeedsOptIn` in
  `internal/workspace/wake_text.go`.

**A login or Node problem** puts the pane in the phase `needs setup` (section 2's `setup` field),
which reads as blocked with the detail "needs setup". The team graph lists it as "Needs setting up"
with one line to act on: sign in with the agent's own tool (Flockdeck never signs in), install Node
20 or newer (for an adapter that needs it) and restart Flockdeck, or install the agent. The state is final, so the pane is started
again after the fix.

**What is recorded.** Nothing, for now. The stream recorder exists (`internal/record/rpc.go`,
[stream recording](rpc-recording-format.md)), but no structured pane starts it, and asking a
structured pane to record answers that it is not available yet. When it is used, it redacts each
line before it is written. It keeps the last 16 KiB of each kind of text so that a label such as
"password:" is seen with its value, and a private key whose END has not arrived keeps its BEGIN line in
that context, so the line is held for 64 KiB of text after the BEGIN and then let go. A BEGIN line that
is not a key, quoted in prose, hides the next 64 KiB of that kind of text. Redaction is best effort.

**A restart** starts a fresh session. The earlier entries stay, then a notice that the agent was
restarted, then the new session's (section 2). A restart that cannot start the new agent keeps them
too.

## 7. What is not covered

- Images' bytes and nested entries in `structuredDetail`.
- A phone, or any window through the relay.
- Sending a prompt. A window uses `paneSend`, which sends a prompt over the protocol to a structured
  pane (`docs/team-graph.md`, section 6).
