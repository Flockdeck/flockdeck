# Team graph: protocol reference

This is the reference for the team graph that Flockdeck sends to a window: the
`graph` message, the commands that ask for it and the neighbouring commands the
team view uses (`paneSend` and the conversation commands). It describes what the
code on the machine Flockdeck runs on does today. For using Flockdeck's team
features, see the in-app help (`F1`). This page is about the messages.

**The graph is a read-only picture of one project.** It places the project's
panes in teams, says what each pane is doing and what it is held up by, lists who
sent mail to whom, and lists what needs the person. Everything in it is stored by Flockdeck (read from the
workspace, the task board or the inboxes) or derived by it (by `team.BuildGraph`
and `team.NeedsYou` in `internal/team`, which read nothing but what they are
given).

The definition of the message is [`team-graph.schema.json`](team-graph.schema.json)
(JSON Schema, draft 2020-12). It names the fields each shape has and which are
always written, and every field's description says where its value comes from and
why it can be absent. This page explains it. Tests validate a realistic graph and
every example on this page against the schema, and check that this page names
every field, command and message type the schema and the server have, so the
three cannot drift apart.

## 1. Commands and who may send them

A window talks to Flockdeck over its control connection (the WebSocket at
`/ws/control`). A command is one JSON object with a `cmd` field naming it. The
fields these commands use are:

| Field | Used by | Meaning |
| --- | --- | --- |
| `cmd` | all | The command's name. |
| `req` | `graphSnapshot`, `graphWatch`, `permissionAnswer` | A token of the window's choosing. It is copied into the `teamResult` that refuses a graph command, or into the `permissionAnswer` reply (section 2), so the window can tell which request it answers. |
| `root` | `graphSnapshot`, `graphWatch` | The directory of the project. Empty means the project on screen. It must name an open project. |
| `id` | `paneSend`, `permissionAnswer`, the conversation commands | The pane's id. |
| `text` | `paneSend` | The text to type. |
| `allowShell` | `paneSend` | The caller means the text for a shell pane. |
| `after`, `before`, `entry`, `q` | the conversation commands | A cursor, an entry id and a search string (section 7). |
| `cardSeq`, `requestId`, `optionId` | `permissionAnswer` | The card being answered, the agent's request id on it, and the option chosen, all as the pane's permission card showed them. |

| Command | What it does | Local window | Window through the relay |
| --- | --- | --- | --- |
| `graphSnapshot` | Answers once with the graph of the project. | yes | refused |
| `graphWatch` | Answers with the graph now and again whenever it changes. One project at a time: watching another replaces the first. | yes | refused |
| `graphUnwatch` | Stops the pushes. | yes | refused |
| `paneSend` | Types text into one pane and presses Enter (section 6). | yes | refused |
| `conversationOpen`, `conversationOlder`, `conversationDetail`, `conversationClose`, `conversationSearch` | Read a pane's conversation (section 7). | yes | yes, for a full device; for a viewer only if the desk has let that device watch panes |
| `permissionAnswer` | Answers a structured agent's permission request with one of the options on its card. | yes | refused |
| `structuredSnapshot`, `structuredWatch`, `structuredUnwatch`, `structuredCancel` | Show a structured pane to a window on the desk, and stop its running turn (`docs/structured-pane.md`). | yes | refused |

The structured pane commands, their message and their limits are in
[`structured-pane.md`](structured-pane.md).

**A local window** is one on the machine Flockdeck runs on. **A window through the
relay** is a paired phone or other device; the graph commands, `paneSend` and
`permissionAnswer` are refused to it whether the device is full or a viewer, and
a viewer is also refused by the table of commands viewers may send
(`commandAccess` in `internal/server/viewerrole.go`, which every command must
have an entry in). The refusals are one place in the code each, marked as the
place to lift when the decision changes. They are there because:

- The phone's part in the team view has not been decided, so the desk is the only
  place the graph is shown.
- A graph carries the text of the prompts panes are waiting on, such as the
  command a permission prompt asks about. That text is clipped but not scrubbed
  of secrets, and team data does not cross the relay in a sealed channel yet.
  The push is scrubbed for a relay window on its way out (`internal/server/relayscrub.go`):
  the graph message is dropped for it, and the state push and agents list carry no
  prompt text, live line, opening prompt or last reply. That scrub is the condition
  for lifting this refusal, with the sealed channel; it does not lift it.
- A permission prompt of a terminal pane is answered with its choice buttons over
  the end-to-end encrypted terminal connection, and that stays the only way to
  answer one from a phone.

The Team panel's own commands (`teamSnapshot`, `teamWatch`, `teamUnwatch`, the
task and message commands) are a different set with the same desk-only rule for a
window through the relay, and are not described here.

## 2. Replies

| Message | Sent when |
| --- | --- |
| `graph` | In reply to `graphSnapshot`, and as the first and later pushes after `graphWatch`. |
| `teamResult` | A graph command could not be done: `{"type":"teamResult","req":"...","op":"graphWatch","ok":false,"error":"..."}`. The usual cause is `no project is open at <directory>`. A refusal of a window through the relay also carries `"code":"deskOnly"`, so that a client need not match the wording of `error`. A window through the relay that sends a graph command gets this and a notice; one that sends `paneSend` gets only a notice; one that sends `permissionAnswer` gets a `permissionAnswer` message and a notice. |
| `teamGone` | To a window that is watching a project that can no longer be read, a closed one above all, so that it shows that and not the last picture: `{"type":"teamGone","root":"...","error":"..."}`. |
| `paneSend` | A `paneSend` was accepted (section 6). A refusal is a notice and not this message. |
| `notice` | A refusal or an error as a line of text: `{"type":"notice","text":"...","error":true}`. |
| `conversationPage`, `conversationAppend`, `conversationDetail`, `conversationSearch` | The conversation commands (section 7). |
| `permissionAnswer` | The result of a `permissionAnswer`: `ok`, or an `error` saying why not. It echoes `req`, `id`, `cardSeq` and `requestId`. A full device through the relay gets it with an `error` and also a notice, because the command is refused to it. A viewer is refused earlier, by the table of commands viewers may send, and gets only that notice. |

Pushes skip a window that has not yet had its `hello` from the server.

## 3. The graph message

```json
{"type":"graph","root":"/home/sam/shop","project":"shop","teams":[{"id":"p1","manager":"p1","members":["p1","p2"],"goal":"Add a basket page","started":"2026-10-20T09:00:00Z","spendUSD":1.42,"spendPartial":true,"caps":[{"pane":"p1","used":1,"max":8}],"needs":[{"id":"permission:p2","kind":"permission","pane":"p2","time":"2026-10-20T09:14:02Z","text":"npm test"}]}],"nodes":[{"pane":"p1","kind":"terminal","depth":0,"team":"p1","children":["p2"],"waitingOn":{"kind":"helpers","text":"1 helper is busy","ref":"p2"},"name":"shop","role":"manager","agent":"claude","status":"working","activity":"working","since":"2026-10-20T09:10:00Z","live":"Reading basket.go","branch":"main","task":{"id":"T4","title":"Basket page","state":"in-progress"}},{"pane":"p2","kind":"terminal","parent":"p1","depth":1,"team":"p1","waitingOn":{"kind":"user","text":"npm test"},"name":"shop-w1","role":"worker","agent":"claude","status":"waiting","activity":"waiting","since":"2026-10-20T09:14:02Z","branch":"basket","dirty":3,"unread":1},{"pane":"p3","kind":"terminal","depth":0,"waitingOn":{"kind":"none"},"name":"shell","shell":true,"status":"idle","activity":"idle"}],"edges":[{"kind":"spawn","from":"p1","to":"p2"},{"kind":"message","from":"p1","to":"p2","id":3,"time":"2026-10-20T09:11:30Z","text":"Start on T4","delivery":"notified"}],"solo":["p3"],"soloNeeds":[]}
```

**A graph is one project, whole.** Every push replaces the last one; there are no
partial updates and nothing to apply in order. A field is either always written or
absent for a reason this page and the schema give. No field is ever `null`.
Counts that are zero are left out.

### The message

| Field | Type | Always | Meaning |
| --- | --- | --- | --- |
| `type` | string | yes | `"graph"`. |
| `root` | string | yes | The project's directory as the workspace spells it. Use it, not the string you asked with, as the key for the project. |
| `project` | string | yes | The name the project switcher shows. Empty when there is none. |
| `teams` | array | yes | One team for each team, in the order of their root panes in the tabs. Empty when there are none. |
| `nodes` | array | yes | One node for each pane of the project, shells included, in the order the tabs show them. A pane id appears once. |
| `edges` | array | yes | See *Edges* below. |
| `solo` | array of pane ids | yes | The panes in no team. |
| `soloNeeds` | array | yes | What needs the person among the solo panes, most urgent first, at most 50. |
| `soloNeedsHidden` | integer | no | How many solo items the bound of 50 cut. |
| `panesHidden` | integer | no | How many panes the bound of 500 nodes cut. |
| `sendMessageUnread` | integer | no | How many `SendMessage` calls of the project's panes were seen and not understood, so their arrows are missing. |
| `boardError` | string | no | Why the task board could not be read. Task chips, waiting on a task, and returned items are then missing. |

### Teams

A team is a root pane and every pane below it by parent. A **root** is a pane
that has no open parent and that has helpers or is in the `manager` role. The
root need not be a manager: any pane that started helpers is one.

| Field | Meaning |
| --- | --- |
| `id`, `manager` | The root pane's id. `id` is stable while the root is open. |
| `members` | The panes of the team, the root first and then breadth first. |
| `goal` | The root pane's opening task as one line cut to 300 characters. Absent for a root started with no opening prompt. |
| `started` | When the root pane's process started (RFC 3339). Absent while the root has no session. |
| `spendUSD` | The estimated spend in dollars of the team's panes that reported a priced figure. 0 when none did. |
| `spendPartial` | `true` when some agent pane that has run reported no priced figure (a figure of zero counts as none), so `spendUSD` is a floor. A shell and an agent that has not started are not counted as missing. |
| `spendIncludesPartial` | `true` when some pane of the team is a structured (ACP) agent that has reported through `usage_update`. That gives the context used, the context size and, if the agent chooses, a cost; it gives no split of tokens into read, written and cached, and no account windows. The total then includes panes that report partly, and a window should say so. A cost of zero (opencode's free tier) adds nothing. A cost with no currency, one that is not a finite number, one above 1,000,000 or one that rounds to nothing in dollars is taken as not reported. A cost in a currency other than dollars is shown on the pane and added to nothing. Spend is not carried across a restart of a pane: the new process reports from zero, so the total can fall, and the partial flags keep it a floor. Absent when no pane does. |
| `caps` | The helper cap use of the root and of every pane with children: `pane`, `used` (open panes below it, nested ones included and exited ones until closed) and `max` (`workspace.MaxHelpers`, 8). The count is the workspace's own, the one that refuses a ninth helper. |
| `needs`, `needsHidden` | The needs-you list for the team (section 5), at most 50, and how many the bound cut. |

### Nodes

| Field | Meaning |
| --- | --- |
| `pane` | The pane's id. |
| `kind` | `terminal` for every pane today, shells included. `structured` is reserved for panes an agent drives over a protocol and is not written yet. A reader must tolerate it. |
| `parent` | The id of the pane that started this one, only while that pane is open. Absent for a root. |
| `children` | The open panes this one started. Absent when there are none. |
| `depth` | 0 for a root and for a pane in no team; the parent's depth plus one below. |
| `team` | The id of the team the pane is in. Absent for a solo pane. |
| `orphaned` | `true` when the pane named a parent that is not open, or is where a parent loop was cut. It is then treated as a root. |
| `waitingOn` | What the pane is held up by (section 4). |
| `name`, `peer`, `role`, `agent` | The pane's name (usually its directory's), the name the agent reported with `flockdeck peer-name`, the id of the role it was started as, and the id of its agent. `peer`, `role` and `agent` are absent until known; `agent` is also absent for a shell. |
| `shell` | `true` for a plain terminal with no agent in it. |
| `status` | The session's status: `starting`, `working`, `waiting`, `blocked`, `idle`, `exited`, or `unknown`. |
| `activity` | What the pane is drawn as doing: `waiting`, `blocked`, `working`, `idle`, `starting`, `exited` or `failed`. Background work that is still running keeps an idle agent reading as `working`. Empty for none. |
| `since` | When the current status began (RFC 3339). Absent for a pane with no session. |
| `live` | The agent's current tool detail, or else its latest reply, on one line cut to 140 characters. |
| `branch`, `dirty`, `untracked`, `ahead`, `behind` | The checkout's branch and its counts of changed files, untracked files, and commits ahead of and behind upstream. Counts are absent at 0. |
| `task` | The pane's current task: its `id`, its `title` cut to 120 characters, and its `state` (`planned`, `assigned`, `in-progress`, `in-review`, `done`, `blocked` or `cancelled`). It is the pane's own open task, in progress first, else in review, else assigned, else blocked. |
| `unread` | Messages waiting unread in the pane's inbox. Absent at 0. |
| `autoReview`, `autoApproved` | `true` when the pane's tool calls are put to auto-review, and how many calls auto-review has let through unasked. |

### Edges

Edges come in this order: a `spawn` edge for each pane whose parent is open, in
node order; then the last 100 `message` edges, newest first; then the last 100
`sent` edges, newest first.

| `kind` | Goes `from` | `to` | Other fields |
| --- | --- | --- | --- |
| `spawn` | a parent | a helper it started | none |
| `message` | the sending pane | the pane whose inbox the message went to | `id`, `time`, `text`, `delivery` |
| `sent` | a Claude session | the pane its `SendMessage` call named | `time` only |

- Both ends of a `message` edge are agent panes of the project. Mail from the
  person or from Flockdeck has no sending pane and is not an edge.
- `text` is the message's first non-empty line, cleaned of control characters and
  cut to 140 characters.
- `delivery` is the newest state of the message: `queued` (in the inbox), `notified`
  (the hook notice of it was given), `woken` (a line was typed into the pane for
  it) or `read` (the agent ran `inbox read`). A state counts as newer by this
  order and not by when it was marked. A reader of data saved before delivery
  states existed treats a missing value as unknown, not as `queued`.
- **A `sent` edge is a claim, not a fact.** It is read from the sending session's
  own hook, and Flockdeck does not carry that message, so it can say a message
  was sent and never that it was read. Every pane's agent holds the same hook
  token, so a session could report a call it did not make.

### Permission prompts

The graph does not carry a prompt's details: a node's `waitingOn.text` is the
command or question, cut to 200 characters. The full view is the `permission`
field of a pane in the state push (`paneView.permission`), and the schema
describes it as `permission`:

| Field | Meaning |
| --- | --- |
| `tool` | The tool the prompt is for. |
| `summary` | One line saying what the tool would do. |
| `command`, `description` | For Bash, the command and the agent's own description of it. |
| `file` | For Edit, MultiEdit and Write, the file. |
| `diff` | What the edit would change, cut at 64 KiB. |
| `options` | The choices of the prompt that are documented for the agent. Claude Code panes waiting on Bash, Edit, MultiEdit or Write list two: Yes and No. Every other agent, every other tool (the plan approval of `ExitPlanMode` among them) and a pane that has not recorded its agent list none, and a window then keeps to the terminal path. Its "Yes, and don't ask again" is not listed, because the rule it names depends on the tool and version. |
| `redacted` | `true` on a card sent to a window reached through the relay: the command, description, file and diff are left out, `options` is empty, and `summary` says what kind of thing was asked and that the rest is on the desktop. Never present for a window on this machine. |

Each option has an `id`, a `name` and a `kind`. The `id` is what a window sends on
the terminal path: `yes` is answered with Enter, the prompt's default, and `no`
with Esc. The `kind` uses the names of the Agent Client Protocol: terminal panes
list `allow_once` (Yes) and `reject_once` (No). `allow_always` and `reject_always`
are unused until structured panes exist.

```json
{"tool":"Bash","summary":"Run npm test","command":"npm test","options":[{"id":"yes","name":"Yes","kind":"allow_once"},{"id":"no","name":"No, and tell Claude what to do differently","kind":"reject_once"}]}
```

## 4. Teams, solo panes and waiting-on

**Placing panes.** A pane whose parent is not open is treated as a root and marked
`orphaned`. If panes name each other as parents, the loop is cut at the first of
its panes in node order, which becomes an orphaned root, so every pane has a root
above it. A pane that is in no team is solo.

**waiting-on** says what a pane is held up by. Exactly one kind applies, the
first in this list:

| Order | `kind` | Applies when | `text` | `ref` |
| --- | --- | --- | --- | --- |
| 1 | `down` | the pane has exited or failed | `exited`, or `failed` and the reason | the pane's own id |
| 2 | `user` | its activity is `waiting` or `blocked` and it has a question or a permission prompt, or it needs setup (see Setup below) | the question or command, or the setup fix, cut to 200 characters | none |
| 3 | `reviewer` | it owns a task in review (not for a shell) | the task and who it is in review by, or that nobody is set to review it | the task id |
| 4 | `task` | it owns a blocked task (not for a shell) | the task's latest note, cut to 200 characters; absent if it has none | the task id |
| 5 | `helpers` | at least one of its open helpers is working or waiting | how many helpers are busy | the first busy helper's pane id |
| 6 | `none` | none of the above | none | none |

Who a task in review waits on is the first of the `reviewer` and `tester` roles
with a live pane other than the owner's. With none, the text says nobody is set to
review it.

## 5. Needs-you items

Each team has a list of what needs the person, and the solo panes have one of
their own (`soloNeeds`).

| `kind` | What it is | `time` | `text` |
| --- | --- | --- | --- |
| `permission` | a pane is waiting on a permission prompt | when it started waiting | the command, cut to 200 characters |
| `ask` | a pane is waiting on a question put to the person | when it started waiting | the question, cut to 200 characters |
| `returned` | a task went from in review back to in progress in the last 24 hours and is still in progress | when it came back | the note it came back with; absent when it had none |
| `conflict` | the conflict radar reports that two panes' work would conflict | when the radar first reported the pair, if known | up to 5 paths, each cut to 80 characters, and "and N more" for the rest |
| `down` | a pane failed, or exited with a code other than 0, and was not closed by the person | when its status began | `failed` and the reason, or `exited with code N` |
| `setup` | a structured pane could not start and needs something from the person: Node, a sign-in, or the agent's program | when its status began | the fix, cut to 200 characters |
| `stale-unread` | a pane that is idle has had unread mail for more than 5 minutes | when the oldest unread message arrived | how many are unread |

- **Order.** Permission and ask first, by how long they have waited, longest first;
  then returned, newest first; then conflict; then down; then setup; then stale-unread, oldest
  first. An item whose time is not known (`0001-01-01T00:00:00Z`) is listed after
  the others of its kind.
- **Down.** A shell is listed only when it failed. A pane that is down is never
  listed as waiting on a prompt, since it cannot be answered.
- **Setup.** The pane stays in the project, with status `blocked`, and the text is
  the fix. There are three causes. Node is missing or too old: the text gives the
  version needed and how to install it, since Flockdeck does not bundle Node. The
  agent wants a sign-in: the text names the methods the agent offered and says to
  sign in with the agent's own tool. Flockdeck does not read, store or pass
  credentials. The agent's program is not installed: the text is the install
  instruction from the agent's entry. The state is final. Nothing is retried after
  the fix, so every text ends with the one step to take next: start the pane again, or for
  Node restart Flockdeck, which has to see the new install. The item goes when
  the new pane starts.
- **Conflict.** `pane` and `other` are the pair in sorted order, whichever way the
  radar listed it. A conflict between panes of two teams is listed in both.
- **Ids.** An item's `id` is made from its kind and its pane (for `returned` its
  task; for `conflict` the two panes in sorted order), so it stays the same while
  the item stands. A window can keep its selection by it.
- **Bounds.** A list holds at most 50 items. `needsHidden` and `soloNeedsHidden`
  count the rest.

## 6. Sending text into a pane

`paneSend` with `id` and `text` types the text into that pane and presses Enter.
It works for any pane open in the workspace, whether or not its tab is on screen,
and writes to that pane alone, whatever the broadcast is set to. Control
characters other than tab, carriage return and line feed are removed first.

The text is written as a prompt. In a pane whose program has not asked for
bracketed paste, each line break types an Enter and so submits what came before
it. In an agent pane, a line starting with `!` or `/` is read by the agent as a
shell or slash command. `allowShell` guards shell panes only and does nothing
about either.

It is refused, with a notice, when:

- the pane is no longer open, or its process has exited;
- the text is over 16 KiB (16384 bytes, counted before control characters are
  removed), or is empty or only white space after they are removed;
- the pane is a shell and `allowShell` is not set;
- the pane is waiting on a permission prompt: a prompt is answered with its choice
  buttons and not by text from another agent. A pane waiting on a question
  (`AskUserQuestion`) is sent the text;
- a write into the pane is still in progress (one at a time for each pane, so a
  pane that has stopped reading cannot collect blocked writers).

An accepted send is answered with a `paneSend` message:

```json
{"type":"paneSend","id":"p2","queued":true,"shell":false}
```

| Field | Meaning |
| --- | --- |
| `id` | The pane the text was typed into, as the command named it. |
| `queued` | The pane was in the middle of a turn when the text was written, so the agent reads it when that turn is over. |
| `shell` | The pane is a plain shell. |
| `waiting` | The pane was waiting on a question, so the text may be read as the answer. Absent otherwise. |
| `routed` | The text was the pane's first prompt and model routing restarted the pane on another model to take it. Absent otherwise. |

The write happens after the reply. `queued` is read before it, so a prompt that
opens in between is not caught.

## 7. The conversation commands

These read what a pane's agent said, from the conversation the agent stored. They
are the same content a terminal shows, and need the same permission as watching
one, which is why a viewer device can send them only when the desk has let it
watch panes. A pane is found only by its id: a window cannot name a file or a
session.

| Command | Fields | Reply |
| --- | --- | --- |
| `conversationOpen` | `id`; `after` (optional entry id) | `conversationPage` |
| `conversationOlder` | `id`, `before` (an entry id) | `conversationPage` of the entries before it |
| `conversationDetail` | `id`, `entry` (an entry id) | `conversationDetail` |
| `conversationClose` | `id` | none |
| `conversationSearch` | `id`, `q` | `conversationSearch` |

- **Open** returns the newest 50 entries, or, when `after` names an entry the
  stream still has, every entry since it. It also makes the window a watcher: the
  server then sends a `conversationAppend` with the changed or new entries
  whenever the conversation moves, until `conversationClose` or the window goes. A
  pane that is working is looked at again every 2 seconds in case a hook event was
  missed. Nobody watching for 30 seconds returns a pane's stream to a light mode
  that keeps previews only.
- **A page** is `{"type":"conversationPage","id":"...","supported":true,"agent":"...",
  "entries":[...],"cursor":"...","atStart":true,"reset":true}`. `cursor` is the id of
  its last entry (or what the window already had, for an empty page); `atStart`
  says no older entry exists; `reset` says the cursor was not one the stream has
  (the desktop restarted, or the conversation was cleared), so the window drops
  what it holds instead of appending.
- **`supported: false`** is the answer for a shell, and for an agent that stores no
  conversation Flockdeck can read. The window should show that and not wait for a
  page. A pane id the workspace does not have gets no reply at all, so a window
  probing for ids learns nothing.
- **Entries** have an `id`, a `kind` and a `ts` (RFC 3339), and the fields of that
  kind: `prompt` (`text`), `reply` (`markdown`), `thinking` (`chars`, `seconds`),
  `tool` (`name`, `label`, `status` of `running`, `ok` or `error`, `summary`,
  `diff`, `hasDetail`), `subagent` (`status`, `description`, `report`,
  `hasDetail`), `compaction` (`turns`), `notice` (`text`) and `image`
  (`mediaType`, `bytes`, `width`, `height`, `hasDetail`). A tool's output over
  4 KiB, a diff over 64 KiB and a thinking block's text are not sent inline: the entry
  has `hasDetail`, and `conversationDetail` returns them in `detail`, as `text`,
  `diff`, nested `entries` or base64 `data`.
  An entry the stream no longer holds (evicted, or the conversation started over)
  is answered with `gone: true` and an empty `detail`, so the window stops waiting;
  the drawer then says "No longer available." The drawer draws at most 4 KiB of a
  tool's output and 64 KiB of a diff, and says "Output truncated: N bytes more are not
  shown." (or "Diff truncated: ...") when it cuts. A structured pane's permission is drawn in the conversation, after the
  running tool it was asked for, as the same card the strip has (the desk answers
  it once, whichever copy is pressed); a window through the relay gets neither.
- **Search** is a plain, case-insensitive substring search, never a regular
  expression. The query is cut to 200 characters. It looks at prompts, replies, and
  the label and summary of tools; never at thinking, a tool's full output, or
  the other kinds. It returns up to 50 matches, newest first, each with `entryId`,
  `kind`, a `snippet` of about 120 characters and the `offset` of the hit in it
  in characters, and `more: true` when there were more. It searches only a
  conversation that some window has already opened; for any other pane it returns
  no matches.

### The task panel

The drawer's task chip opens a panel that shows the task a pane works on. On the
desktop window it sends `taskGet` (see section 1) and draws the title, state,
owner, branch, creator, last change, text and notes of the `teamResult` that
answers it. The notes are drawn 20 at a time. All of it is drawn as text. The
panel's "Go to pane" button opens the owner's pane when it is open and is not the
pane already shown. A window reached through the relay sends no `taskGet`, since
task text is not sent there, and shows only the chip's id, title and state. The
task data has no dependencies, so the panel shows none.

## 8. Limits and throttling

| What | Limit |
| --- | --- |
| Pushes of a watched graph | At most one every 500 milliseconds. A change inside the gap is sent when it is up. The reply to `graphWatch` counts as a push. |
| Pushes with no change | None: a graph identical to the last one sent to the window is not sent. A window whose queue was full is sent the graph again. |
| Clock-driven looks | Every 30 seconds while any window watches, because two things depend on the clock alone: mail waiting 5 minutes in an idle pane, and a returned task passing 24 hours. |
| Panes in a push | 500 nodes; `panesHidden` counts the rest. |
| Edges | 100 `message` edges and 100 `sent` edges. |
| `SendMessage` calls remembered | The last 200, in memory only. |
| Needs-you items | 50 for each team and 50 for the solo panes. |
| Text | A waiting-on text 200 characters; a live line 140; an edge's first line 140; a task title 120; a team's goal 300; a pane's name 80, its peer name 80, its branch 120 and its agent id 80. All are cut by characters and have control characters and runs of white space turned into single spaces. |
| `paneSend` text | 16 KiB, one write in progress for each pane. |
| Helper cap | `max` is 8 for each pane. |
| Relay scrub cost | A message to a window reached through the relay is decoded and encoded again once for each such window, in `controlClient.write`. A window on this machine is written the bytes as they were. |

## 9. Compatibility

- There is no version number in the message yet.
- **Fields may be added** within this shape, in the schema and on this page in the
  same change. A reader should **ignore a field it does not know** and a value it
  does not know of an enumerated field (a `kind` of node, edge or needs-you item,
  a `waitingOn.kind`). The schema is strict because it describes what Flockdeck
  writes, not because a reader should reject more.
- **No field is ever `null`.** A field is written, or absent for a reason given
  here.
- A field is removed, renamed or changes meaning only in a change that updates the
  schema, this page and the golden file the tests compare a two-team project
  against, so the change is on purpose.

## 9a. The relay form: relayTeams

A window reached through the relay is never sent the graph message, and the graph
commands stay refused to it (section 1). It is sent `relayTeams` instead: a message
of its own that the server pushes to such a window without being asked, when a
window comes in, when the form changes (at most every 500 ms) and every 30 seconds
at the latest. A window on this machine is never sent it. A viewer is sent it only
if it may watch panes.

```text
{"type":"relayTeams","v":1,"projects":[{
  "id":"p1","project":"flockdeck",
  "teams":[{"id":"<manager pane id>","label":"Team 1","manager":"<id>","members":["<id>"],
            "needs":[{"pane":"<id>","kind":"permission"}]}],
  "panes":[{"id":"<id>","parent":"<id>","depth":1,"team":"<id>","kind":"terminal",
            "label":"Worker 2","status":"waiting","role":"worker","activity":"waiting",
            "wait":{"kind":"user","prompt":"permission"},"unread":0}],
  "solo":["<id>"],"soloNeeds":[]}]}
```

Every field is an enumerated word, an id Flockdeck made (`^[A-Za-z0-9_-]{1,64}$`),
a count, or a label made by the server (`Team 1`, `Worker 2`: the built-in role and
the pane's place among those of that role). The one free text is `project`, which is
redacted and cut to one line. `role` is a built-in role id, or `other`. `wait.kind`
is `user`, `down`, `reviewer`, `task` or `helpers`; `wait.prompt` is `permission` or
`question` for a wait on a person; `wait.on` is the first busy helper, for `helpers`.
A need's `kind` is `permission`, `question`, `returned`, `conflict`, `down`, `setup`
or `unread`.

Not in it: pane names, peer names, goals, live lines, branches, task titles, edge
texts, the texts of needs and waits, permission and question text, agent names and
paths. A structured pane has `kind` and `status` and nothing else. The names of
teams and panes are open: until the user decides they may cross, a pane is "Worker 2".
This message withholds names, but the existing `state` message to a relay window still carries pane names (redacted and cut to 40 characters), so names are not hidden from the relay by this form.

Two locks hold this. The form is built from the graph by field, and the scrub
registered for the type (`scrubRelayTeams`) checks every field again against the
allow lists and makes the labels itself, so a mistake in the builder does not put
text on the wire. Tests list the fields by name, plant a marker in every free-text
field of a graph, and read a real relay socket.

**Team structure crosses the relay in the clear** until the sealed team socket
exists. `remote.Capability` is 2 with this form. A client that reads a capability
below 2 should show no team view; a desktop at capability 1 still sends the roster
as before.

## 10. What is not covered

- **Anything beyond a message's delivery state.** The log of what Flockdeck did
  to deliver mail is not in the graph.
- **Structured panes.** `kind` can say `structured`, but no pane is one in this
  version of the graph push; their commands (`permissionAnswer`) are listed in
  section 1 and are desk-only.
- **Merging, and everything on the phone.** No merge command exists in the graph's
  commands, and the graph commands are refused to windows through the relay
  (section 1).
- **The Team panel's commands and messages**, which the older panel uses.
- **Where the conversation stream is held and how long.** The command and its
  messages are described in section 7; the hub's memory use is an
  implementation detail.
