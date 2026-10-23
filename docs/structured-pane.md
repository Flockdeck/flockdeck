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
The drawer still draws the push and has no Conversation tab. The server, though, now answers the
conversation commands (`conversationOpen`, `conversationOlder`, `conversationDetail` and
`conversationSearch`) for a structured pane, from the runtime's entries: pages of 50, older pages by
cursor, `conversationDetail` for an entry's text (up to 64 KiB each for text and diff), and
`conversationAppend` as the entries change. They are in memory, so they go as far back as the bridge's
cap (10000 entries) plus the history kept across restarts (2000). A restart is sent as a
`conversationPage` with `reset` set, and a cursor from before it is answered the same way. A
`conversationDetail` for an entry the runtime no longer holds answers with a one-line notice. The
`structured` and `structuredDetail` messages stay until the new Conversation view uses these. Every
text is drawn as text.

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

**opencode is started to ask.** opencode, by default, runs a shell command, a write or an edit without
sending a permission request at all, so no card would appear and the pane would look supervised when it is
not (`opencode-e2e.md` has the run that showed it). Flockdeck therefore starts it with
`OPENCODE_CONFIG_CONTENT={"permission":{"bash":"ask","edit":"ask"}}` in its environment (`launchEnv` in
the adapter manifest, see [ACP adapters](acp-adapters.md)), and shell commands and file edits arrive as
permission cards. This was checked against opencode 1.19.3: its own `debug config` shows the setting taking
effect, and `TestRealOpencodeEndToEnd` run with no `opencode.json` anywhere gets a card for the shell command.

What this does to opencode's own configuration, as 1.19.3 resolves it: the sources, lowest first, are the
user's global `opencode.json`, a file named by `OPENCODE_CONFIG`, the project's `opencode.json`, and
`OPENCODE_CONFIG_CONTENT`. A permission key set by a later source replaces the whole value of that key from an
earlier one, so a plain `bash` and `edit` setting would replace the user's and the project's own, patterns and
denials included. Flockdeck therefore builds the value from what opencode resolves (below): the user's and the
project's rules for `bash` and `edit` stay in their order, an allow becomes an ask, and a deny stays a deny. A rule
such as `"rm *": "deny"` still denies. Every other permission key (`read`, `webfetch`, `external_directory` and the rest) is left as the user set it, and
opencode's own defaults for them still apply, so a web fetch or a search is not asked about unless the user's
configuration says so. Flockdeck sets nothing to allow, only to ask. The card offers "Always" as opencode
sends it, and choosing it for a command allows that kind of command for the rest of the session, including one
the user had denied in their own configuration; Flockdeck does not take that option off the card.

**The gate does not hold where a repository sets permissions for an agent.** Checked on 1.19.3 with a fixture
project: `{"agent":{"build":{"permission":{"bash":"allow","edit":"allow"}}}}` in the project's `opencode.json`,
and the same under `permission:` in the front matter of `.opencode/agent/build.md` (and of a custom agent's
file), put their rules after the inline value in the agent's resolved rules, and the last matching rule wins.
`opencode debug agent build` shows the allow after the ask. `TestRealOpencodeEndToEnd` run against the first
fixture got no permission card: the shell command ran and the turn ended. So the permission cards gate commands
only in a repository you trust not to set an agent's permissions to allow. A project's `opencode.json` or
`.opencode` folder can also load plugins and MCP servers, which run code, so no permission setting makes an
opencode pane safe in a repository you do not trust: open an unfamiliar repository in a terminal pane, not a
structured one.

**The launch check.** When a structured opencode pane starts, Flockdeck runs `opencode debug agent build` in the
pane's own directory, with the environment the agent gets but without the setting. That prints the rules opencode
resolves there: the user's, the project's and any agent-level ones. Flockdeck builds `OPENCODE_CONFIG_CONTENT` from
the rules for `bash` and `edit` as described above, starts the agent with it, and runs the same command again with it
set. If `bash` or `edit` can still run some command without asking (the last rule for every command allows it, or a
rule for some commands allows it after the last rule for every command), or either run fails or prints something
Flockdeck cannot read, the pane is flagged. If the first run cannot be read or turned into a value (too many rules,
a rule it does not know, a value over 12 KiB), the manifest's plain value is used instead, which asks for
everything and drops the user's denials, and the pane is flagged only if the second run still shows an allow or
cannot be read. The check is `internal/acp/permprobe`; the manifest entry's `permissionProbe` names the command, the
variable and the tools. It costs two extra starts of opencode, run one after the other before the agent starts:
about 1.9 s each on the machine it was measured on (opencode 1.19.3, Windows), so a pane takes about 4 s longer to
start. The pane shows as starting until it is done.

Until the check has run the manifest's `runsWithoutAsking` stands, so the flag shows from the start and goes if the
check finds nothing. `TestRealOpencodeFixtures` (set `FLOCKDECK_REAL_OPENCODE` to the opencode program) runs it
against a real opencode in fixture projects: an empty project, the user's granular shell and a denied edit, a
project's allow, a project's own denials, an agent-level allow in `opencode.json` and one in
`.opencode/agent/build.md`. The first four end with the tools asking and the denials in place; the last two are
flagged.

The drawer header of the pane says, while it is flagged, "This agent may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask, or could not check. A project's opencode.json or .opencode folder can also load plugins and MCP servers that run code, so no permission setting makes an opencode pane safe in a repository you do not trust." The same sentence about plugins and MCP servers
holds for every opencode pane, flagged or not; it is in the help page.

A terminal opencode pane is not started this way and keeps its own configuration. The old window shows a stub
for a structured pane and no warning.

**What a structured pane loses against a terminal pane:**

- A phone gets no push notification for a pending permission, because it could not answer it.
- `permissionAnswer` is refused to a window reached through the relay, and to a device that may only
  view. So are the commands of section 1.
- A relay window is told the pane's name, status, directory, branch, project, agent and model, and no
  text the agent wrote. The scrub in section 1 removes what the agent said.
- A window reached through the relay receives no conversation text from any pane (T112).
  `refuseRemoteText` refuses `conversationOpen`, `conversationOlder`, `conversationDetail` and
  `conversationSearch` to it for every pane, and it is sent no `conversationAppend`,
  `conversationDetail` or `conversationSearch`, so a structured pane's conversation does not reach it
  by this path either (`TestARelayWindowGetsNothingOfAStructuredPanesConversation`).
- The old window shows a stub line and sends it nothing; the pane is for the new interface's drawer.
- No export, since the conversation is not stored where Flockdeck reads it. The conversation
  commands read it from memory (see the drawer section above).
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
- No startup sweep. If Flockdeck is killed outright, a structured agent and what it started are not
  looked for by the next run. On Windows the job object that holds the agent's tree is closed with
  Flockdeck and takes it down, unless the job could not be made (security software that already put
  the process in a job, or a very old Windows; see `internal/proctree/tree_windows.go`), and then the
  agent is left as it is on Unix. On Unix the agent is the leader of a session of its own and nothing
  ends it: a well-behaved agent exits when its standard input closes, which it does, but a tool it
  started may keep running. The sweep that exists (`ReapStaleWorktreeProcesses`) reads the record of a
  terminal pane's process, which a structured pane does not write. Decision (T110): not added now,
  because it needs a record of the agent's pid and start time and a test on Unix, and a clean shutdown
  already ends the whole tree (`TestCloseEndsWhatTheAgentStarted`). A parent-death signal on Linux
  could close the gap later. After Flockdeck was killed, look for leftover agent or tool processes
  whose parent is gone (parent pid 1, or the user's systemd process on Linux, or by the agent's name) and end them by hand.

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
