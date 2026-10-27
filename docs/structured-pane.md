# Structured pane: protocol reference

This is the reference for the commands a window uses to show a structured pane, an agent that
Flockdeck drives over the Agent Client Protocol instead of in a terminal. It is the backend of the
drawer. It carries the pane's state: phase, status, activity, plan, usage, the permission card and setup
problems. The pane's conversation is not in it; a window reads that with the conversation commands
(`docs/team-graph.md`, section 7), as for any other agent pane.

The shape of the messages is `api/schema/structured-pane.schema.json`. A test checks messages the server
writes against it, and checks the limits below against the code.

## 1. Commands and who may send them

A window sends a command as one JSON object with a `cmd` field, over its control connection.

| Command | Fields | What it does | Local window | Window through the relay |
| --- | --- | --- | --- | --- |
| `structuredSnapshot` | `id`, `req` | Answers once with a `structured` message. | yes | refused |
| `structuredWatch` | `id`, `req` | Answers with the `structured` message now, and sends it again whenever it changes. One pane at a time: watching another replaces the first. | yes | refused |
| `structuredUnwatch` | none | Stops the pushes. | yes | refused |
| `structuredSet` | `id`, `req`, `kind`, `setting`, `value` | Changes the mode, the model or one setting of that pane (API 1.17, cap `structuredSettings`). `kind` is `mode`, `model` or `config`; `value` is the id of the mode or model, or the value of the option; `setting` is the option's id for `config` and empty otherwise. Only a mode, model, option and value in the lists the pane last pushed are accepted, so a value the agent has since replaced is refused. It does not wait for the turn to end. Answers with a `teamResult` (`ok`), or an error with fixed text: the pane is not a structured pane, the setting is not on offer, the agent is not running, or the agent did not change it. What the agent said about a refusal is not passed on. The push carries the new value once the agent has accepted it. | yes | refused |
| `structuredFiles` | `id`, `req`, `q` | Lists file names under the pane's working directory for the prompt box's `@` picker (API 1.18, cap `structuredFiles`). `q` is the text after the `@`, cut to 200 bytes; it matches the file name first, then the path, then the letters in order, without regard to case. Answers with a `structuredFiles` message: `id`, `req`, `q` as cleaned, `files` (at most 50 slash-separated paths relative to the pane's directory, best match first), `more` when the list was cut, and `error` with fixed text when the directory could not be read or the window asked more than 40 times in 10 seconds. A pane that is not a structured pane gets a `teamResult` error. In a git working tree the names are those git lists as tracked or not ignored (`.gitignore` is honoured); elsewhere, or when git's list is over 4 MiB, a breadth-first walk reads at most 50,000 entries, skips directories that start with a dot and `node_modules`, and stops after 5 seconds. A symlink or junction is never followed or listed, a name is sent only if it resolves to a regular file inside the directory, and a name that is not valid UTF-8, has a control character, is over 300 bytes or has a `.` or `..` element is dropped. Only names are read, never contents. | yes | refused |
| `structuredExport` | `id`, `req`, `dir`, `thinking` | Saves the pane's conversation as a Markdown file in the folder `dir` (API 1.20, cap `structuredExport`). `dir` must be a full path to a folder that exists; the file is named `flockdeck-<pane name>-<date and time>.md` by the server, created new (an existing file is never replaced), and cannot be placed anywhere but inside that folder. On Unix it is created with the owner's permission only; on Windows it takes the folder's permissions, so it is not private. Only the first 20 letters of the pane's name are kept, so the time is always in the name. A network share (`\\host\share`, also with slashes) and a device path (`\\.\`) are refused; a drive path and `\\?\X:\` are allowed. The file holds the person's prompts, the agent's replies, each tool call as its label, status and summary, helper agents' reports, mail between agents (marked with its direction, peer and delivery state) and notices; tool output, diffs and images are left out, and thinking only when `thinking` is true and the runtime still holds it. The title is a code span. Every body is a quoted block (each line starts with `> `), so text from the agent cannot make a heading or a turn of the person's; inside it `![` is written `\![` and `<` is written `&lt;`, so images and HTML do not load or run when the file is opened in a viewer, and links and code fences are left as they were. Text passes through the baton's redactor first (the pane's secret environment values, the keys Flockdeck holds, the checkout's `.env` files, and the shapes of keys); if it cannot be made whole (the key store cannot be listed, or a `.env` file cannot be read or is over 1 MiB) nothing is written. A panic while exporting is still answered with fixed text. Answers with a `teamResult`: `ok`, `path`, `entries`, `redacted` (values replaced) and `warning` when the file was cut at 8 MiB or some text was too large or took too long to redact and was replaced with a `too-large-to-scrub` mark; or an `error` with fixed text. One export of a pane runs at a time. | yes | refused |
| `structuredCancel` | `id`, `req`, `keepQueue` | Ends the running turn of that pane and drops the prompts waiting behind it, unless `keepQueue` is true (API 1.19), when the waiting prompts stay and the next one is sent; an open permission request of the pane is answered cancelled. Answers with a `teamResult` (`ok`), or an error if the pane is not a structured pane or has nothing running. Only that pane's own session is touched. | yes | refused |
| `structuredQueueEdit` | `id`, `req`, `item`, `text` | Replaces the text of the waiting prompt `item` (API 1.19, cap `structuredQueue`), keeping its place. The text is cleaned and limited as a `paneSend` text is (16 KB, control characters stripped, not empty). | yes | refused |
| `structuredQueueRemove` | `id`, `req`, `item` | Takes the waiting prompt `item` out of the queue. | yes | refused |
| `structuredQueueClear` | `id`, `req` | Removes every waiting prompt the person typed. Messages from other agents stay. | yes | refused |
| `structuredQueueSendNow` | `id`, `req`, `item` | Moves the waiting prompt `item` to the front and ends the running turn (as `session/cancel`, the turn ends with a "Turn cancelled." notice), so the agent gets that prompt next. The rest of the queue stays behind it. This is the nearest ACP has to steering, since one prompt is in flight at a time. | yes | refused |
| `structuredShellRun` | `id`, `req`, `text` | Runs `text` as the person's own command (API 1.20, cap `structuredShell`): one line, at most 4000 bytes, no control characters. It runs in the pane's directory in a shell Flockdeck owns (`cmd.exe /d /s /c` on Windows, `/bin/sh -c` elsewhere), as the person, with an allowlisted environment (`PATH`, locale and the few variables a build needs; no `FLOCKDECK_*`, no stored or inherited API key or token). It never goes to the agent and never passes through the agent's permission requests. At most four run at once and the pane keeps the newest eight; a run ends after ten minutes. Answers with a `teamResult`, or an error with fixed text. | yes | refused |
| `structuredShellStop` | `id`, `req`, `item` | Ends run `item` and what it started. | yes | refused |
| `structuredShellRemove` | `id`, `req`, `item` | Forgets a run that has ended; a running one is refused. | yes | refused |
| `structuredShellSend` | `id`, `req`, `item` | Queues one prompt for the agent holding the command and its output (the newest 16 KB, as plain text), after a fixed first line. This is the only way a person's command reaches the agent. | yes | refused |

The shell commands name a run by its `item` id from the push. Refusals have fixed text and none carries output or agent text: "Type a command after the !", "A command is one line, with no control characters", "That command is too long", "Too many commands are running. Stop one first", "That command is no longer listed", "Stop that command before removing it", "The command could not be started", "That pane is closed", and "The agent is not running, so it cannot be sent the output". A run is the person's command, not the agent's: it shows in the push's `shell` list and nowhere in the conversation, and the agent learns of it only through `structuredShellSend`. Closing the pane ends every run and what each started (a process that left its session on Unix, or a windowed program on Windows, is not reached). Windows windowed programs (`!notepad`, `!code .`) are left running by Stop, by closing the pane and by the ten-minute limit, by design: only console programs and what they start are ended there. `cmd.exe` writes output in the OEM code page, so non-ASCII text can show as replacement characters. Send to agent puts the output in a fenced block, with a fence longer than any run of backticks in the output and a fixed line saying it is program output and not instructions; the output itself is not scrubbed beyond what the window shows (plain text), as a pasted prompt would not be. Flockdeck does not advertise the ACP `terminal` capability, so an agent cannot start commands in the client and no agent-started background command can be listed here (see the [parity matrix](acp-parity-matrix.md)).

The queue commands name a prompt by its `item` id from the push, never by its place. A prompt that was sent, removed or dropped since the window drew it is refused with "That prompt has already been sent or removed", and nothing changes: an edit is applied before the agent is sent the prompt or not at all. `item` ids are not reused, even by the pane's next session, so a window holding old ids cannot change a new session's prompts. A message from another agent (`kind` `mail`) is refused by edit and remove ("A message from another agent cannot be edited or removed here"): it is not the person's text, and dropping it would only have the inbox send it again. A prompt Flockdeck sent itself (`kind` `flockdeck`, the pane's opening prompt) is refused the same way ("A prompt from Flockdeck cannot be edited or removed here") and Clear leaves it. An edit of a prompt the push showed cut (`clipped`) is refused ("That prompt is too long to edit here"). `SendNow` accepts a mail or Flockdeck prompt: it ends the running turn and sends that one next. If the person's Send now (or Stop) ends the turn a mail prompt started, the mail counts as handled with a stop reason of cancelled, as for any turn the person cancels (T187), not as undeliverable. All answer with a `teamResult` (`ok`) or an error with fixed text.

`id` is the pane's id. `req` is a token of the window's choosing; it is copied into the answer.

**A local window** is one on the machine Flockdeck runs on. **A window through the relay** is a
paired phone or other device. It is refused all of these commands whether the device is full or a viewer,
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
the other roster fields. No text the agent wrote is sent. The `structured` message is dropped
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
| `queue` | The waiting prompts, next first (API 1.19): `id`, `pos`, `kind` (`user`, `mail` or `flockdeck`), `state` (`queued`), `peer` (mail only), `text` (cut to 2000 bytes) and `clipped`. For `user` the text is exactly as typed, so an edit starts from it; for `mail` the text and `peer` have format and direction characters (Unicode Cf, such as U+202E) removed. A message from another agent is `kind` `mail` with the sender in `peer`; its body never carries the sender, and a window draws `peer` and `text` apart. Prompts the person sent while the turn runs, and mail handed over by the delivery service, are in the same list; mail past the 16 the runtime holds waits in the store and is not listed. |
| `shell` | The person's own commands, the oldest first, at most 8 (API 1.20): `id`, `cmd` (one line, cut to 500 characters), `state` (`running`, `exited`, `stopped`, `timeout` or `failed`), `code` (only for `exited`), `text` and `cut`. `text` is the output as plain text: escape sequences, carriage-return overwrites, control characters other than line break and tab, and format characters (U+202E and the like) are removed, and it is cut to its newest 8 KiB (`cut` says the start was let go; the runtime keeps at most 64 KiB). A window draws it as text only. |
| `err` | The last thing that went wrong, as one cleaned line. It says what last went wrong and not that something is wrong now; show it with the phase and status. |
| `setup` | While the phase is `needs setup`: `kind` (`login`; before API 1.16 also `node` and `install`), a `message` (the cause) and a `fix` (what to do about it, when there is advice). |
| `plan` | The agent's plan rows. |
| `card` | The permission request that is open, with the agent's options. Answer it with `permissionAnswer`. |
| `usage` | `used` and `size` of the context, and `cost` and `currency` when the agent reports one. |
| `title` | The session's title, when the agent set one. |
| `commands` | The slash commands the agent offers: `name`, and `description` and `hint` when it gave them. |
| `modes`, `models` | The `current` id and the `available` choices (`id`, `name`, `description`) of the block the agent sent with `session/new` (Gemini sends both). Absent when it sent none. If the agent sends a `current_mode_update` before any modes block, `modes` has the `current` id and an empty `available`. |
| `configOptions` | The settings the agent exposes (opencode and Codex: model, effort, mode): `id`, `name`, `category`, `type`, the `current` value as text and the `values` it can take (`value`, `name`, `description`, `group`). |
| `planMarkdown` | A plan the agent sent as markdown (Codex `plan_update`). `plan` holds rows. A pane can have either. The markdown may contain remote images or links, so a window must not load images automatically. |
| `capabilities` | What the agent said it can do at initialize, while the phase is `ready`: `loadSession`, `image`, `audio`, `embeddedContext`, `mcpHttp`, `mcpSse`, `listSessions`, `resumeSession`, `forkSession`. |

The session fields (`title`, `commands`, `modes`, `models`, `configOptions`, `planMarkdown`, `capabilities`) came with API 1.12, and a server that names `structuredSession` in its `caps` sends them. Agents differ in what they offer: opencode and Codex put the model, effort and mode in `configOptions`; Gemini sends `modes` and `models` and no config options. A window draws whatever is present and treats an absent field as "the agent offers none". The set of values for `configOptions` is the agent's and changes mid-session (a `config_option_update` replaces the whole list), so a window reads the current list each time instead of caching it. `structuredSet` changes them. A boolean option is a `configOptions` entry with `type` `boolean`; it is set with the value `true` or `false`.

How each call goes to the agent: `kind` `mode` is `session/set_mode`, `model` is `session/set_model` (the protocol still lists it as unstable), and `config` is `session/set_config_option`, whose answer carries the whole list of options and replaces the pane's. An agent that answers `-32601` has no such method and the change is refused. Flockdeck's own Claude and Codex adapters list `modes` and `models`, as Gemini does; an agent such as opencode lists `configOptions`. At most four changes wait on the agent at once, and one that the agent does not answer ends after the client's setup timeout (60 seconds by default) and gives its slot back. A change is not queued behind the running turn: it is sent at once, even mid-turn, and what the agent does with it then is the agent's affair. Open permission cards stay open and are answered as before; a mode change does not close or answer them.

The pane's unasked marking (`runsUnasked` in the pane view) follows the mode. When the agent's current mode, or the current value of one of its select options, reads as running without asking (the id, name or description holds a word such as `yolo`, `bypass`, `dangerous`, `full-access`, `dontAsk` or "without asking"; an accept-edits mode does not count, since it still asks for commands), the pane is marked, whoever made the change and whether or not the launch check found the agent safe. This is a guess from words (`LooksUnasked` in `internal/structured/settings.go`). The interface asks for a confirm before it sends a change to such a value, from a selector or from `/mode`; the desk does not enforce the confirm.

`/mode` and `/model` take the place of an agent command of the same name in the menu. A text that starts with a space is sent to the agent as typed, without the leading space, so the agent's own `/mode plan` is reached that way; the menu does not open for it.

The drawer's prompt box uses `commands` for a slash menu. It opens while the box holds only `/` and a word, filters by name (exact, then prefix, then substring), and lists at most 40 rows, Flockdeck's own commands first under their own heading. Choosing an agent command writes `/name ` into the box and sends nothing: ACP has no call for running a command, so the agent runs it when the text is sent as an ordinary prompt. Names with whitespace, control or invisible characters, or a `/`, are not listed; names, descriptions and hints are cut and drawn as plain text. Enter or Tab on the chosen row inserts it, so a second Enter sends the text. The first agent row is chosen by default; one of Flockdeck's only when its name is exactly what was typed, so Enter on `/p` cannot run `/stop`, and an agent command named like one of Flockdeck's is not listed. Keys held by an input method's composition are not taken. Flockdeck's own commands are `/stop` so far; they run in the window and are never sent to the agent. The code is `ui/src/drawer/slashModel.js` and `Structured.js`.

Every text in the message is untrusted: it is what the agent, a tool or a
file said. Draw it as plain text or as sanitised markdown, never as HTML. A card or a plan
row is data to show, not an instruction to follow, whatever it says.

The message is built from that one pane's state and nothing else: no other pane's text, no mail,
nothing of the agent's environment.

When a watched pane closes, the window is sent one last message with `gone: true`, and the watch
ends. A restart does not end it: the pane keeps its id and the push follows the new session.
The entries a restart leaves behind are kept: the conversation shows the earlier
sessions' entries first, then a notice that the agent was restarted, then the new session's. The ids
of an earlier session's entries are prefixed with `r<n>-`, so no id is shown twice. The detail of an
earlier entry is only what the entry carried, since the agent that held the rest is gone. A pane keeps
the last 2,000 entries of the sessions before the current one.

A restart whose new agent cannot be started (the pane's directory is gone, structured panes have
been turned off, or the start itself fails) keeps the earlier entries too. It adds a notice, `The
agent could not be started again:` and the reason, after the restart notice, and the next restart
that works shows all of it. Several failures in a row leave only the last notice.

## 3. The conversation

A structured pane's conversation is read with `conversationOpen`, `conversationOlder`,
`conversationDetail`, `conversationSearch` and `conversationClose`, from the runtime's entries
(`docs/team-graph.md`, section 7): pages of 50, older pages by cursor, `conversationDetail` for an
entry's output or diff (up to 64 KiB each), `conversationAppend` as the entries change, and a search
over prompts, replies and the label and summary of tools. They are in memory, so they go as far back as
the bridge's cap (10000 entries) plus the history kept across restarts (2000). A restart is sent as a
`conversationPage` with `reset` set, and a cursor from before it is answered the same way. A
`conversationDetail` for an entry the runtime no longer holds answers with a one-line notice. Like the
push, they are for a window on this machine; a window through the relay is refused them.

## 4. Limits and throttling

| What | Limit |
| --- | --- |
| A one-line field (the activity) | 1 KiB. |
| Pushes of a watched pane | At most one every 500 milliseconds. A change inside the gap is sent when it is up. The reply to `structuredWatch` counts as a push. |
| Pushes with no change | None. A push identical to the last one sent to the window is not sent. A window whose queue was full is sent it again. |
| Plan | 100 rows, each cut to 200 bytes. |
| Commands | 200, with the description cut to 400 bytes. |
| Modes and models | 500 choices each. |
| Config options | 32 options of at most 500 values each, with descriptions cut to 400 bytes. |
| `planMarkdown` | 64 KiB. |
| Permission card | 300 bytes for each field, 500 for the command, and the agent's options with 100 bytes for each field. |
| `err` | 200 characters. |
| `conversationDetail` text and diff | 64 KiB each. |
| `structuredFiles` | 50 names of at most 300 bytes in an answer; 40 asks in 10 seconds for each window; 4 MiB of git's list, 50,000 entries walked, 5 seconds. |
| Panes watched at once, over all windows | 16. A watch past that is refused with `Too many structured panes are being watched. Stop watching one first`. |

Every cut is made between characters. A text is never cut inside a UTF-8 sequence, bytes that are not
UTF-8 are replaced, and control characters other than a line break and a tab are removed. A
one-line field has its line breaks and tabs turned into spaces.

A watch holds no goroutine of its own. The push is made from the state push the server already
makes whenever the workspace changes.

## 5. The drawer view

The drawer draws a structured pane from this watch and from its conversation
(`drawer/Structured.js`). The header carries a "structured" label beside the name. The panel shows the
activity line, which is the tool still running (read from the conversation) with an icon for its kind
and the first file its summary names, or the push's activity text, or "Idle". It shows the context used of
its size, and the cost when the agent reported one. Under them is the pane's conversation, drawn by the
same component as a terminal agent's Conversation tab (`drawer/Conversation.js`, `drawer/conv/`): the
same rows, paging with Older, search, Copy, and runs of tool rows folded into "N steps". A structured
pane has no Conversation tab of its own, since this is it. Replies are sanitised Markdown: nothing in
one is put into the page as markup, a link is made only for http and https addresses, and a reply over
20,000 characters is cut with a note saying how much is not shown. Opening a tool row asks for its
output or diff with `conversationDetail` (4 KiB of output and 64 KiB of diff are drawn, and the cut is
said), and a permission waiting on the pane is drawn in the list after the tool it was asked for, as the
same card the top of the drawer has. Export is not offered for a structured pane (there is no stored
transcript to export), and the toolbar says so. Every text is drawn as text.

The prompt box (T212) takes several lines: Enter or Ctrl/Command+Enter sends and Shift+Enter adds a line,
and the box grows from two to ten rows. Up on the first line and Down on the last step through the pane's
earlier prompts and back to the draft; the history (50 prompts of at most 8 KiB) and the draft (16 KiB)
are kept per pane in the window's local storage, for the 30 panes used last, and in memory when storage
is missing or full. `@` after white space asks `structuredFiles` (after a 120 ms pause, one answer matched
by its `req`) and lists the names; choosing one writes `@path ` as text. When `capabilities.image` is set,
a picture pasted, dropped or chosen is scaled to under 600 KiB, sent with `attachImage`, and its saved path
is written as `[image: path]`; one is in flight at a time. A window reached through the relay has none of
this. There is no setting for the send key.


## 6. Using a structured pane

This section is for the person using one. It says what is true today and does not promise more.

**It is a beta and it is opt-in.** Structured panes are off until `"structuredPanes": true` is in
`prefs.json` in the state directory. There is no setting for it in the window. Until it is on, the
picker does not offer a structured agent, and a layout that holds one shows the pane with a message
saying so. Turning it off takes effect when the agents are read again or a pane is restarted; a
running pane is not ended by it. Today the built-in entry that is offered is `opencode-acp`
(opencode, structured). The Gemini and Codex entries are hidden until they have been checked end to
end. There is no entry for Claude Code yet; a Go adapter for it is in progress (T204, see
[ACP direction](acp-direction.md)). [ACP adapters](acp-adapters.md) lists the tested versions.

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
such as `"rm *": "deny"` still denies. That is about the user's and the project's rules, not the built-in agents' own:
the value applies to every agent, so opencode's built-in `plan` agent, whose own rules deny `edit`, asks for an edit
instead of denying it. That is a permission card and not an unasked edit, and `plan` is reached only if a session is
switched into it. This was read from opencode 1.19.3 in an empty project: `opencode agent list` without the value
lists `plan`'s `edit` rules as `*` deny, then two allows for plan files, and with the value set the same rules
are followed by `bash` `*` ask and `edit` `*` ask, so the last matching rule for `edit` is ask. That is the
resolved rules, not an edit attempted by `plan`. The built-in agents' defaults come before the value and a project's
`agent.NAME.permission` comes after it, which is why the second still wins (see below). Every other permission key (`read`, `webfetch`, `external_directory` and the rest) is left as the user set it, and
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

**The launch check.** When a structured opencode pane starts, Flockdeck runs `opencode agent list` in the pane's own
directory, with the environment the agent gets but without the setting. That prints every agent opencode knows there
and the rules each resolved: the user's, the project's and any agent-level ones. Flockdeck builds
`OPENCODE_CONFIG_CONTENT` from the `build` agent's rules for `bash` and `edit` as described above, starts the agent
with it, and runs the same command again with it set. **Every** agent in the second listing is looked at, not only
`build`: a project can name another agent as the one a session starts in (`default_agent`), and can give a subagent
that the task tool starts its own rules, and `build`'s rules say nothing about either. If `bash` or `edit` can still
run some command without asking in any agent (the last rule for every command allows it, or a rule for some commands
allows it after the last rule for every command), or the second run fails or prints something Flockdeck cannot read,
the pane is flagged. This is slightly conservative: an agent the session never uses still flags the pane. It covers
`bash` and `edit` only; `webfetch`, `read` and the others are as before. If the first run cannot be read or turned
into a value (no `build` agent, too many rules, a rule it does not know, a value over 12 KiB), the manifest's plain
value is used instead, which asks for everything and drops the user's denials, and the pane is flagged only if the
second run still shows an allow or cannot be read. The check is `internal/acp/permprobe`; the manifest entry's
`permissionProbe` names the command, the agent, the variable and the tools.

It costs two extra starts of opencode, run one after the other before the agent starts: about 2 s each on the
machine it was measured on (opencode 1.19.3, Windows; a fixture run of both took 4 s), so a pane takes about 4 s
longer to start. The pane shows as starting until it is done. Each run is stopped after 30 s and when the pane is
closed, so a hung opencode holds the pane in starting for up to 60 s. opencode loads a project's plugins when it runs,
so a project's plugin code runs three times at pane start (the two checks and the agent). That is no different from
the agent starting, and it is one more reason the sentence below is true for every opencode pane.

Until the check has run the manifest's `runsWithoutAsking` stands, so the flag shows from the start and goes if the
check finds nothing. `TestRealOpencodeFixtures` (set `FLOCKDECK_REAL_OPENCODE` to the opencode program) runs it
against a real opencode in fixture projects: an empty project, the user's granular shell and a denied edit, a
project's allow, a project's own denials, an agent that denies, and the flagged cases: an agent-level allow in
`opencode.json` and in `.opencode/agent/build.md`, a `default_agent` with an allow (in `opencode.json` and in a file),
and a subagent (`general`, `explore`) with an allow. The five that are not flagged end with the tools asking and the denials in place.

The drawer header of the pane says, while it is flagged, "This agent may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask, or could not check. A project's opencode.json or .opencode folder can also load plugins and MCP servers that run code, so no permission setting makes an opencode pane safe in a repository you do not trust." The same sentence about plugins and MCP servers
holds for every opencode pane, flagged or not; it is in the help page.

A terminal opencode pane is not started this way and keeps its own configuration.

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
- No export, since the conversation is not stored where Flockdeck reads it. The conversation
  commands read it from memory (see the drawer section above).
  A restored pane does resume the agent's own session when the agent can (section 6, resume).
- Mail reaches a structured agent as a `session/prompt` of two text blocks, with no opt-in. The agent
  is not given `FLOCKDECK_API`, `FLOCKDECK_TOKEN` or `FLOCKDECK_PANE` (names starting `FLOCKDECK_` are
  never passed to an agent), so it cannot run `flockdeck inbox`, and its briefing has its own Messages
  section that does not mention the command. The first block is `Flockdeck message from agent
  "<role>":`, or `the user` for a message marked as the person's. The second is the message text on
  one line, with escapes, control characters and line breaks removed, in full up to the 8 KiB a message
  may hold, and starts with the fixed marker `Text of the message: `, so no block but the first ever
  starts with the prefix whatever the body says. The sender label is cut at 32 characters. A message is never typed and never marked read.
  This is a trust change: text written by another agent reaches an agent that may run commands as a
  user turn. The guards are the fixed first block in a block of its own, the single line, and the
  briefing's untrusted-data disclaimer (`promptBlocks` in `internal/workspace/promptdelivery.go`).

  Delivery is tracked on the message (`team.Message.Route` is `prompt`, set when the message is made
  to a pane that is, or was saved as, a structured pane):

  | State | Meaning |
  | --- | --- |
  | `queued` | In the store. The prompt delivery service hands it to the pane's runtime, which keeps 16 prompts behind the running turn; messages past that wait in the store and are handed over as room appears. |
  | `delivered` | The prompt was sent and its turn began. This is the nearest ACP gets to "accepted": there is no separate acknowledgement before the turn. |
  | `handled` | The turn ended with `end_turn`, `max_tokens` or `max_turn_requests`. The stop reason is stored on the message. |
  | `undeliverable` | Three turns began and none was handled. |

  A cancel the person asked for (Cancel was called during the turn) counts as handled and keeps the
  stop reason `cancelled`. A turn that ends `cancelled` with nobody asking, or `refusal`, or with a stop reason the protocol does not define (stored
  as `unknown`), puts the message back to `queued` with the stop reason kept and the attempt counted.
  So does an agent that exits or is restarted during the turn (stored as `interrupted`), or one that
  answers the prompt with an error, and a prompt dropped from the runtime's queue by Cancel. When
  Flockdeck starts, a message that was `delivered` and never ended is queued again, since the process
  that held it is gone. Results are matched to messages by the message id the prompt was sent with and
  by the runtime that was sent it, never by order, so a runtime that was replaced cannot change what a
  new one holds. A prompt the runtime refuses before any turn counts as an attempt, so it ends `undeliverable` after
  three. Prompt-route mail is in no unread count, `inbox read` returns it without marking anything, and if
  its pane turns out to be a terminal pane (not structured and not saved as structured) the unfinished
  messages move to the terminal route. The wake switches (`flockdeck wake`) pause delivery; the mail stays queued and its
  sender can see it. The store is the audit log: `team-events.jsonl` gets a `delivery` event for each
  state with `stop` and `attempt`, and no message text. The sender sees the state of what it sent on the
  graph's message edges and in the Team panel's message list (API 1.13, `docs/team-graph.md`).
- Mail is shown in the conversation (API 1.14, cap `mailEntries`). A message delivered to a structured
  pane is an entry of kind `message` with `dir` `in`, `peer` (the sender's label: the person as "the
  user", an agent by its role or name, cleaned to one line and cut), `msg` (the message id), `delivery`
  and `text` (the body, the same one line of plain text the agent was sent). It is made when the turn
  that carries the message begins, in place of the `prompt` entry that turn would otherwise get, so mail
  is never drawn as something the person typed, and no body can make it one. A message the pane sends
  (the Flockdeck MCP server's `send_message`, or `flockdeck msg send` from the pane) is an entry with `dir`
  `out`, `peer` (the recipient's label), the body and `delivery` `queued`, made when the message is
  stored. Both entries follow the message's delivery state (`queued`, `delivered`, `handled`,
  `undeliverable`) as the inbox reports it, in place, so the entry changes and the id stays. Mail from
  the person or from a terminal pane makes no `out` entry, since there is no conversation to put it in.
  A restart keeps the entries as history with the state they had; they are not updated afterwards.
  Search does not look at message entries. A window that does not know the kind draws it as an unknown
  entry.
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

**A login problem** puts the pane in the phase `needs setup` (section 2's `setup` field),
which reads as blocked with the detail "needs setup". The team graph lists it as "Needs setting up"
with one line to act on: sign in with the agent's own tool (Flockdeck never signs in). The state is
final, so the pane is started again after the fix.

**An agent that cannot be found or started** is the phase `failed` with `err` set to "the agent is
unavailable" and no `setup`. The same fixed text (`agent.UnavailableText`) is in the agent picker
(`unavailable`), in a refused spawn and in a pane that could not start. It names no program and no
runtime, gives no reason, repeats nothing the agent wrote and has no install steps, because Flockdeck does
not install agents (API 1.16). The `install` field of the agent list and of the start dialog's agents is removed in the same version.

**What is recorded.** Nothing, for now. The stream recorder exists (`internal/record/rpc.go`,
[stream recording](rpc-recording-format.md)), but no structured pane starts it, and asking a
structured pane to record answers that it is not available yet. When it is used, it redacts each
line before it is written. It keeps the last 16 KiB of each kind of text so that a label such as
"password:" is seen with its value, and a private key whose END has not arrived keeps its BEGIN line in
that context, so the line is held for 64 KiB of text after the BEGIN and then let go. A BEGIN line that
is not a key, quoted in prose, hides the next 64 KiB of that kind of text. Redaction is best effort.

**Resume.** The layout saves the agent's session id for each structured pane (`acpSession` in the
layout file; it is not part of the window API). When the pane is restored, the runtime reopens that
session before it makes a new one:

- If the agent said `loadSession` at initialize, it sends `session/load`. The agent replays the
  conversation as `session/update` calls before it answers. The client marks the updates that arrive
  while the load waits for its answer (`Update.Replay`), and the bridge takes only those into the
  transcript, between `BeginReplay` and `EndReplay`: the person's messages become prompt entries, the
  agent's become replies, thinking and tools. A replay does not start a turn, so the status stays idle.
  `BeginReplay` empties the transcript first, a replay update outside that window is dropped, and a
  replay is held to the entry and text limits with prompts counted (a hostile replay cannot grow the
  transcript without bound). A tool the old session left running is shown with no status.
- Else, if the agent has `sessionCapabilities.resume`, it sends `session/resume`. No history comes, so
  the transcript starts empty while the agent still has the conversation.
- If `session/load` is answered with an error and the agent has `resume`, `session/resume` is tried
  next. A load that gets no answer within `LoadTimeout` is not followed by a resume: the agent may
  still be sending the replay.
- If the agent has neither, or the call fails for any reason other than the agent exiting, needing a
  login or being closed (a stale or unknown id, for one), a new session is made and the conversation
  gets the notice "Could not resume, started a new session." That text is fixed. What the agent said
  about the failure is not shown. After a failure with no answer (a timeout) the id of that session
  is dead: nothing the agent sends for it later is taken, so a replay that arrives late does not
  reach the transcript.
- A prompt entry made from a replay is marked `Replayed` (Go only, not in the window protocol). It is
  the agent's text and not words typed in the pane, so the baton leaves it out of the prompts it
  carries.

The layout keeps the id while the agent is still starting or could not start, and after a reopen that
got no answer (timeout), because the agent may still have that conversation. It takes the new id after
the agent answered with an error. A layout saved before the id was kept starts a new session without a notice.

**A restart** starts a fresh session, and does not resume: it is the person asking for a new one. The earlier entries stay, then a notice that the agent was
restarted, then the new session's (section 3). A restart that cannot start the new agent keeps them
too.

## 7. What is not covered

- Export of a structured pane's conversation.
- A phone, or any window through the relay.
- Sending a prompt. A window uses `paneSend`, which sends a prompt over the protocol to a structured
  pane (`docs/team-graph.md`, section 6).
