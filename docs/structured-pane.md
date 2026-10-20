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

`id` is the pane's id. `req` is a token of the window's choosing; it is copied into the answer.

**A local window** is one on the machine Flockdeck runs on. **A window through the relay** is a
paired phone or other device. It is refused all four commands whether the device is full or a viewer,
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

These commands are refused to a relay window, but this is not the only way a structured pane's
content could reach one: the general state push (`state`, `agents`, the team graph and the
conversation messages) is a separate path and today still carries a structured pane's last reply,
permission prompt and question to a relay window. Closing that is the work of the scrub of what goes
to the relay (task X1), and until it ships the content of a structured pane does not stay on the
desk.

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
| `setup` | While the phase is `needs setup`: `kind` (`login` or `node`) and a `message`. |
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

## 5. What is not covered

- Images' bytes and nested entries in `structuredDetail`.
- A phone, or any window through the relay.
- Sending a prompt. A window uses `paneSend`, which sends a prompt over the protocol to a structured
  pane (`docs/team-graph.md`, section 6).
