# Pane recording: transcript format reference

This is the reference for the transcript files Flockdeck writes: when a pane's
**Record** toggle is on, and when a pane's transcript is exported with *Export
transcript* or `flockdeck recordings export`. For turning it on and what it is
for, see *Recording a pane* in the in-app help (`F1`) or the README. This page
is about the files.

**A transcript is made from the conversation the agent stored itself** (for
Claude Code, `~/.claude/projects/<folder>/<conversation>.jsonl`), by one piece of
code, whether the conversation is still going and the pane is being recorded, or
is over and it is exported. A recording of a conversation and an export of it are
therefore the same bytes (section 8). Anything the agent only tells Flockdeck
while it runs, such as a permission dialog opening or the pane's status, is not
in the stored conversation and so is not in a transcript.

The definition of the format is [`recording-line.schema.json`](recording-line.schema.json)
(JSON Schema, draft 2020-12). It is strict: it names the fields each line type
has and which are required, nothing else is allowed, and every field's
description says where its value comes from and why it can be missing. This page
explains it. Tests validate every line the writer produces, for the export and
the recording alike, and every example on this page, against the schema, and
check that the schema and this page name every field and line type the code
has, so the three cannot drift apart.

Format version: **2**.

## 1. Where the files are

```text
<state directory>/recordings/<project>-<hash>/<start>-<conversation>.jsonl
<state directory>/recordings/<project>-<hash>/exports/<start>-<conversation>.jsonl
```

- `<state directory>` is Flockdeck's per-user directory: `%AppData%\flockdeck`
  on Windows, `~/Library/Application Support/flockdeck` on macOS,
  `$XDG_CONFIG_HOME/flockdeck` (default `~/.config/flockdeck`) on Linux.
  `flockdeck recordings -dir` prints the `recordings` folder. Nothing is ever
  written into a project's repository.
- `<project>` is the last element of the directory the stored conversation
  recorded (the project's, or a worktree's for an agent working in one), with
  anything outside `A-Za-z0-9._-` turned into `-` (at most 40 characters), and
  `<hash>` is eight hex digits of a hash of that directory, so two directories
  with one name get two folders.
- `<start>` is the time of the conversation's first event, UTC, as
  `20261001T101530Z`, and `<conversation>` is the first eight characters of the
  conversation's id. That name is the line's `session`.
- A recording is in the project's folder, and an export, unless it was given a
  path of its own, in its `exports` folder.
- On Linux and macOS the folder is created `0700` and files `0600`. On Windows
  the files inherit the recordings folder's permissions, under your profile:
  readable by you and by a local administrator. A transcript is saved on this
  machine, not private to you (section 4, *File permissions*).

**One file is one conversation.** It is written from the conversation's first
event: turning recording on for a pane whose conversation is already going writes
what has been said so far, and then follows it. It ends when recording is turned
off, the pane is closed, the size cap is reached, or the agent goes on in a new
conversation (`/clear`), which starts a file of its own. A pane left recording
across a restart of Flockdeck has the file written again from the conversation's
beginning, which gives the same lines and the same name, so it is the same file
and not another. A file is never added to once its transcript has ended, except
by being made again, and a finished transcript is only replaced by one that has
every event it had, in order (it is written beside it, under a name of its
own, as `<name>.jsonl.<12 hex digits>.new`, synced to disk, and moved into place
when finished; otherwise it is left as it was). If a program has the earlier file
open and it cannot be replaced, the new transcript is removed, the earlier file is
as it was, and Flockdeck says so. Such a `.new` file that nothing is writing and
that is a day old, as a quit or a crash leaves, is removed the next time a
transcript is made in that folder; no other file is. If the conversation's
first event is now a different one, so that the name is different, the earlier
finished transcript of the same conversation is removed when the new one is
finished: a conversation is one file. Two panes cannot record one conversation
at once, as they would interleave: the second is refused.

A pane whose agent stores no conversation Flockdeck can read has nothing to
write: turning recording on for it is refused, saying so, and no file is made.

**There is no rotation.** A file has a size cap of **16 MiB**. When the next line
would pass it, the writer ends the file with one `recording_truncated` line, and
a recording is switched off (the window says so). An export is cut there too, and
says so. A conversation that is already longer than that when recording is turned
on is cut in the same place, so the recording is the first 16 MiB of it, exactly
as an export of it is, and recording ends at once with a notice saying the
conversation was already longer than a transcript can be. (The confirmation says
so beforehand.)

**Retention** is applied to a project's folder each time a recording is made in
it: files with a modification time older than **30 days** are deleted; then, if
more than **100** files or **256 MiB** remain, the oldest are deleted until
neither is exceeded. The file just opened is never deleted, and files in the
folder that do not end in `.jsonl` are left alone. **Exports are kept until you
delete them by hand**: retention does not count them, and making one never
deletes another file (exporting a conversation again replaces its earlier
export, and nothing else). Nothing else deletes transcripts: delete the files you do not want kept.


## 2. The line format

- JSON Lines: one JSON object per line, separated by `\n`, no blank lines, no
  trailing comma, UTF-8. Each line is written with a single write call.
- Every line is a complete, self-contained object with the envelope below, so a
  line can be read, filtered and validated without the lines around it.
- **`v` is 2.** A reader of this page must **refuse a file whose `v` is not 2**
  and not guess: `v` changes when a field is removed, renamed or changes
  meaning, never when one is added.
- **Fields and line types may be added** within version 2, in the schema and on
  this page in the same change. A reader should therefore **skip a line whose
  `type` it does not know** and **ignore a field it does not know**. The schema
  is strict because it describes what this version writes; that is not a reason
  for a reader to reject more.
- **A last line that is not valid JSON: ignore it.** A crash can cut the last
  line off (section 6).
- **No field is ever `null`**, an empty string standing for "unknown", or a zero
  standing for "unknown". A field is written, or it is absent, and the reason it
  is absent is in the tables below and in the schema. `isError` and `interrupted`
  are written on every `tool_result`, true or false. `redacted` and `clipped` are
  flags that appear only when something was redacted or cut, so their absence
  means nothing was.

## 3. Fields

### The envelope

Every line has these.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `v` | integer | yes | Format version: `2`. |
| `seq` | integer | yes | The line's number within its file: 1 for the first, then 2, 3, with no gaps. |
| `time` | string | yes | When the event **happened**, as the agent's own record has it (stored by the agent): RFC 3339 in UTC with a `Z` and up to nanosecond precision (`2026-10-01T10:15:30.123456789Z`). It never goes back from one line to the next (see *Time* in section 6). |
| `session` | string | yes | The transcript's id: `<start>-<conversation>` (section 1), which is the file's name without `.jsonl` where Flockdeck named it. The same wherever the file is put. |
| `conversation` | string | yes | The agent's own id of the conversation the transcript is of (stored by the agent). A transcript is of one conversation, so it is the same on every line of a file. It changes when the user runs `/clear`, which starts a file of its own. |
| `agent` | string | yes | The agent's id (`claude`, ...) as `flockdeck agents` lists it. Known to Flockdeck, not stored in the conversation. |
| `project` | string | no | The project's name: the last element of the first directory the stored conversation records (derived by Flockdeck). Absent only when the stored conversation records no directory at all. |
| `gitBranch` | string | no | The git branch the stored entry behind the line records (for Claude Code, the entry's `gitBranch`). On **every** line type, each with the entry's own, so a conversation that changes branch has each line's: `recording_started` has that of the first event, `recording_stopped` that of the last, `recording_truncated` that of the event that did not fit, and `conversation_title` that of the entry before it (a stored title has none of its own). Absent only where that entry records none. Redacted and cut at 8 KiB like other strings. |
| `cwd` | string | no | The working directory the stored entry records: **a full path, which usually contains the account's name** (`C:\Users\sam\work\shop`). On the same lines as `gitBranch` and absent for the same reason. It is the agent's directory at that moment, so it changes when the agent moves into a worktree or a subfolder, and a conversation can have several. Redacted and cut at 8 KiB; see section 4, *What redaction does not catch*. (`project`, by contrast, is only the last element of the first one.) |
| `agentVersion` | string | no | The agent's own version that wrote the stored entry behind the line (for Claude Code, the entry's `version`, such as `2.1.286`). On the same lines as `gitBranch` and absent for the same reason. |
| `type` | string | yes | The line type, one of the types below. |
| `redacted` | boolean | no | `true` if anything in the line was replaced by redaction (section 4). Absent means nothing was. |
| `clipped` | object | no | For each field that was cut, its length in bytes before cutting (section 4). Absent means nothing was. |

The identity in the envelope (`conversation`, `agent`, `project`) is worked out
from the stored conversation alone, in one place, whichever way the transcript is
made: recording, *Export transcript* and the command line all give the same
lines for the same conversation. Nothing about a pane is in them. A pane's name
changes, and a saved layout can have it and the pane's model stale, so a line
that carried them could not be the same however it was made; there is no pane
name in a transcript for that reason, and the `model` an assistant turn has is its
own, read from the stored conversation.

**Why there is no header line.** What is the same on every line (`agent`,
`conversation`, `project`) is repeated on each, so a line can be used alone, and
`agentVersion`, `cwd` and `gitBranch` as the conversation began are on
`recording_started`. What a header could add and a line may not carry is how the
transcript was made (live or exported), Flockdeck's own version, and when it was
made: all of those differ between two transcripts of one conversation, and the
two must be the same bytes.

### Which fields are on which line

`●` is a field the line type always has. `○` is one it has unless the *reason it
is absent* in the type's section applies. A blank is a field the type never has.
The envelope's fields are not repeated.

| Field | started, stopped | truncated | user_prompt | assistant_message | tool_call | tool_result | conversation_title | conversation_compacted |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `text` | ● | ● | ● | ● |  |  |  |  |
| `model` |  |  |  | ○ | ○ |  |  |  |
| `usage` |  |  |  | ○ | ○ |  |  |  |
| `stopReason` |  |  |  | ○ | ○ |  |  |  |
| `tool` |  |  |  |  | ● | ○ |  |  |
| `toolUseId` |  |  |  |  | ○ | ○ |  |  |
| `input` |  |  |  |  | ○ |  |  |  |
| `output` |  |  |  |  |  | ○ |  |  |
| `isError`, `interrupted` |  |  |  |  |  | ● |  |  |
| `title` |  |  |  |  |  |  | ● |  |
| `trigger`, `tokensBefore`, `tokensAfter` |  |  |  |  |  |  |  | ○ |

### Line types

#### `recording_started`

First line of every file, at the time of the conversation's first event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `start of the transcript`. It says nothing of how the transcript was made, which is the same however it was. |

```json
{"v":2,"seq":1,"time":"2026-10-01T10:15:30.1Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"recording_started","text":"start of the transcript"}
```

#### `recording_stopped`

Last line of a file that was ended deliberately, at the time of the last event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `end of the transcript`. |

```json
{"v":2,"seq":41,"time":"2026-10-01T10:31:02.8Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"recording_stopped","text":"end of the transcript"}
```

#### `recording_truncated`

Last line of a file that reached the size cap. There is no `recording_stopped`
after it. Its `seq` is that of the line that did not fit, which is not written,
so `seq` still has no gap.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | What happened, in words, with the cap. |

```json
{"v":2,"seq":9120,"time":"2026-10-01T11:02:44.0Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","type":"recording_truncated","text":"the transcript reached its size cap of 16 MiB and ended here"}
```

#### `user_prompt`

What the user typed. The entries Claude Code writes into the conversation for
itself are not prompts and are left out: a slash command and its output, an
injected `<system-reminder>`, a background task's `<task-notification>`, the
note a conversation continued from a summary opens with, "Request interrupted by
user".

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The prompt. Cut at 32 KiB. |

A `user_prompt` has no `model`: the person wrote it, not a model, and the model
that answers it is not known when it is written.

```json
{"v":2,"seq":3,"time":"2026-10-01T10:15:40.2Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"user_prompt","text":"run the tests and fix what fails"}
```

#### `assistant_message`

A message the agent said: **every** text message in the conversation, including
the ones between tool calls ("Let me look at the client first"), in order. A
message that came with tool calls is written before them.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The message. Cut at 32 KiB. |
| `model` | string | no | The model that produced the turn (stored by the agent; for Claude Code, the entry's `message.model`). Written on `assistant_message` and `tool_call` lines only, so a conversation that switches model mid-way has each turn's own. Absent when the stored turn names no model, or only a placeholder such as `<synthetic>` (what Claude Code stamps on a notice it wrote itself). Never the model a pane is set to now. |
| `usage` | object | no | The tokens the model's reply used; see *Token usage and stop reason* below. Its keys are `inputTokens`, `outputTokens`, `cacheCreationInputTokens` and `cacheReadInputTokens`, all integers, each written only if the stored reply gives it. On the first line of a reply only. |
| `stopReason` | string | no | Why the reply ended, in the agent's word; see below. Once per reply. |

```json
{"v":2,"seq":19,"time":"2026-10-01T10:16:55.9Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","model":"claude-opus-5-5","type":"assistant_message","text":"All 212 tests pass.","usage":{"inputTokens":6,"outputTokens":212,"cacheCreationInputTokens":1450,"cacheReadInputTokens":30705},"stopReason":"end_turn"}
```

**Token usage and stop reason.** Claude Code stores one reply of the model as
several entries, one for each content block (a thinking block, some text, a tool
call), and repeats the whole reply's usage and stop reason on each of them. A
transcript does not repeat them: for each reply, `usage` and `stopReason` are
written **once**, on the first `assistant_message` or `tool_call` line the reply
gives. (Not on the reply's first entry, which is often a thinking block and gives
no line at all.) The rest of the reply's lines have neither. So:

- **To add up what a conversation used, sum `usage` over every line that has
  one**, key by key. Each reply is counted once, and no deduplication is needed.
  `inputTokens` is the input that was not cached; `cacheCreationInputTokens` and
  `cacheReadInputTokens` are the cached input written and read, and the four are
  separate, not parts of each other. Each key is
  there only if the stored reply gives that count, and a missing key means the
  count is unknown, never `0`: a consumer adding up usage should treat a missing
  key as adding nothing and not as a zero it was told, and should know a total
  with a key missing on some replies is a lower bound for that kind. The numbers are the agent's, as
  stored, and are not a bill: they say nothing of price, of the other things
  Claude Code stores with them (service tier, speed, per-iteration breakdowns),
  or of subagents, whose entries are left out (section 5).
- A reply whose entries give no line at all (only thinking) is not counted, as it
  has no line to carry it. A reply that has no id in the stored conversation is
  counted once per entry.
- `stopReason` is the agent's word (`end_turn`, `tool_use`, `max_tokens`, ...). It
  is left out where the stored reply has none (`null`), and goes on the first line
  of the reply whose entry has one: the same line as `usage`, unless the stored
  entries gave the reason later.
- Neither is on a `user_prompt` or a `tool_result`.
- The assumption behind "once, on the first line": a line is written when its
  entry arrives and cannot be changed afterwards. So `usage` goes on the reply's
  first line, taken from the first entry that produces a line and has it (a
  thinking-only entry produces no line, so its usage is dropped and the next
  entry's is written), and `stopReason` goes on the first line whose entry has
  one. Later entries of the reply add nothing. That is exact only while every
  entry of a reply repeats the same numbers, which is what Claude Code does so
  far as it was checked: one real Claude Code conversation (1,198 assistant
  entries, 602 message ids, 405 of them with more than one entry, none
  disagreeing on usage or stop reason) and the repository's fixtures. That is all
  the data it covers. If Claude Code ever wrote a partial count on an early entry
  (while streaming) and the final one on a later entry, the first line would
  carry the partial count and the total would come out short, with nothing in the
  transcript to correct it. So treat a per-reply or per-conversation total as
  exact only under that assumption, and do not expect a transcript to be
  corrected after it is written. If Claude Code stops repeating the numbers, what
  `usage` and `stopReason` mean would have to be decided again.
- The exporter counts the entries whose usage or stop reason differs from the
  first entry seen for the same reply (which may be a thinking entry that writes
  no line), not from the value that was written; the count is
  `transcript.ReplyDisagreements`, and the tests in `internal/session/transcript`
  fail when a fixture conversation has a reply whose entries disagree. Entries
  without a message id are each counted as a reply of their own, so the count
  cannot see disagreement there.


#### `tool_call`

The agent calling a tool, before it runs.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | yes | The tool's name: `Bash`, `Edit`, `Read`, an MCP tool's full name. |
| `toolUseId` | string | no | The agent's id for this call, to pair it with its `tool_result`. Absent only where the stored block gives none. |
| `input` | any JSON | no | The tool's input as the agent gave it, usually an object. Each string in it is redacted and cut at 8 KiB. Absent only where the stored call has none. |

A `tool_call` has `model`, `usage` and `stopReason` as an `assistant_message` does:
the turn's own `model`, and `usage` and `stopReason` on the first line of the
reply.

```json
{"v":2,"seq":4,"time":"2026-10-01T10:15:42.0Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","model":"claude-opus-5-5","type":"tool_call","tool":"Bash","toolUseId":"toolu_01","input":{"command":"go test ./...","description":"Run the tests"}}
```

#### `tool_result`

A tool finishing, or failing. It has no `model`: a tool made it, not a model. The
call it answers has the model, and `toolUseId` joins the two.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | no | The name of the call it answers. **Derived by Flockdeck**: the stored result does not name the tool, so it is looked up by `toolUseId` among the calls already read. Absent only when that call is not in the transcript. |
| `toolUseId` | string | no | The id of the call it answers (stored by the agent). Absent only where the stored result gives none. |
| `output` | string | no | What the tool returned: the text the agent was shown. (The tool's own structured result, which can hold a whole file an edit was made to or an image as base64, is not used.) Cut at 8 KiB. For a failure, the error. Absent where the tool returned no text. |
| `isError` | boolean | yes | Whether the tool failed (stored by the agent). Written on every `tool_result`, `true` or `false`. |
| `interrupted` | boolean | yes | Whether it failed because the user stopped it. **Derived by Flockdeck** from the stored error text, which says it was interrupted by the user: the agent stores no flag for it. `true` only with `isError` `true`. Written on every `tool_result`. |

```json
{"v":2,"seq":5,"time":"2026-10-01T10:15:58.4Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"tool_result","tool":"Bash","toolUseId":"toolu_01","output":"ok  \tshop/api\t0.412s","isError":false,"interrupted":false}
```

#### `conversation_title`

The conversation being given a title. Claude Code names a conversation after a
few messages (`ai-title` entries in its stored conversation, which repeat the
current title many times). This line is written when a title first appears and
again whenever it **changes**, never for a repeat, so the last one is the
conversation's title as stored. The stored entry has no time of its own and no
branch, directory or version, so the line has those of the entry before it, and
it is not written before the transcript's first event: a title that arrives then
is written the next time the agent repeats it.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `title` | string | yes | The title. Redacted and cut at 8 KiB like other strings. |

```json
{"v":2,"seq":5,"time":"2026-10-01T10:15:40.2Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"conversation_title","title":"Add a retry to the client"}
```

#### `conversation_compacted`

Where earlier history was summarised to make room (Claude Code's `/compact`, or
the automatic one when the conversation gets too long). It is written where the
stored conversation marks it (a `compact_boundary` entry). The summary itself is
**not** recorded, here or as a `user_prompt` or `assistant_message`: the entry
that holds it is one the agent wrote for itself, which a transcript leaves out
(see `user_prompt`). Lines before this one are the history that was summarised,
and the agent goes on from the summary, not from them. The numbers are the
agent's, are not counts of lines, and are left out where the entry has none.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `trigger` | string | no | What started it, in the agent's word: `auto` or `manual`. Absent where the entry records none. |
| `tokensBefore` | integer | no | The conversation's size in tokens before it was summarised. Absent where the entry records none. |
| `tokensAfter` | integer | no | Its size in tokens after. Absent where the entry records none. |

```json
{"v":2,"seq":88,"time":"2026-10-01T11:02:11.4Z","session":"20261001T101530Z-0123abcd","conversation":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","project":"shop","gitBranch":"main","cwd":"/home/sam/shop","agentVersion":"2.1.286","type":"conversation_compacted","trigger":"auto","tokensBefore":970192,"tokensAfter":22085}
```

## 4. Redaction and clipping

Both are applied to every line just before it is written, so nothing reaches the
file without them. **Redaction is best effort**: it works by patterns, and a
secret that matches none and sits in text that does not call it a secret is
recorded as it was said.

### Redaction

Replaced with the string `[redacted]`:

- a private key block, `-----BEGIN ... PRIVATE KEY-----` to its `END` line (or
  to the end of the text if it is cut off);
- the password in a URL: `scheme://user:pw@host` becomes `scheme://user:[redacted]@host`;
- the credential in an `Authorization`-style header: `Bearer x` and `Basic x`
  keep the scheme, and the credential becomes `[redacted]`;
- the value in `NAME=value`, `NAME: value` and `"name": "value"` where the
  name contains `secret`, `token`, `password`, `passwd`, `pwd`, `api_key` or
  `apikey` (with `_` or `-` allowed), `access_key`, `private_key`, `credential`,
  `authorization`, or `auth_token`, `auth_key`, `auth_header`, in any case;
- every value under a key with such a name inside a tool's `input` object;
- tokens by shape: `sk-ant-...`, `sk-...`, GitHub (`ghp_`, `gho_`, `ghu_`,
  `ghs_`, `ghr_`, `github_pat_`), GitLab `glpat-`, AWS access key ids (`AKIA` or
  `ASIA` and 16 characters), Slack `xox?-`, Google `AIza...`, npm `npm_...` and
  JSON Web Tokens.

Replaced with `[withheld: a secret file]`:

- the `output` of a tool call whose input names a secret file, and the
  string fields of such a call's `input` other than its path or command. A file
  is a secret file by the name check Flockdeck's auto-review uses: `.env` and
  `.env.*`, `id_*` that is not `.pub`, `.npmrc`, `.netrc`, `_netrc`, `.pgpass`,
  `.git-credentials`, anything with `credential` in its name, and `.pem`, `.key`,
  `.p12`, `.pfx`, `.ppk`, `.keystore` and `.jks` files. The path is a
  `file_path`, `filePath`, `path` or `notebook_path` field, or a word of a Bash `command`.
  The file's name is kept.

A line with any of this has `"redacted": true`.

### What redaction does not catch

Redaction is a pattern list over text, not a guarantee. Treat a transcript as
possibly holding a secret even when no line is marked `redacted`, and do not
record a pane you will handle a secret in if you intend to share the file. In
particular:

- **Unconventional names.** A secret named something not on the list above
  (`SESSION_COOKIE`, `DATABASE_URL`, `SLACK_WEBHOOK`, `dsn`, `db_pass`) with no
  recognisable token shape is kept as written.
- **Encodings and splits.** A secret that is base64- or URL-encoded, or split
  across two fields or two lines, is not recognised: redaction sees the text as
  it is written, and does not decode or reassemble it.
- **Secret files by resolved content.** The secret-file check above is a name
  check only. A symlink inside the project that points at a real secret, a
  secret stored under an ordinary name, or a read whose path sits in a tool
  field other than `file_path`/`filePath`/`path`/`notebook_path`/`command` is not withheld;
  its contents then fall back to pattern redaction, which catches only the
  shapes and names above.
- **Paths and branch names.** `cwd` is written as the stored entry has it, so a
  line has the full path of the agent's directory, which usually includes the
  account's name and can include a client's or a project's. `gitBranch` can name a
  ticket or a person. Both go through the redaction above, which finds secrets and
  not names, so neither is hidden. If a transcript is to be shared, `cwd` is the
  field to strip (`jq -c 'del(.cwd)'`).
- **A cut secret.** A value long enough to be clipped before it reaches
  redaction can leave a fragment too short to match a token pattern, and the
  fragment is kept.

### File permissions

Transcripts are written user-only (`0600`, in a `0700` folder) on Linux and
macOS. On Windows the file inherits the permissions of the recordings folder
under your profile, which is readable by you (and by a local administrator, as
any file under your profile is), not by other standard users. The honest claim
is "saved on this machine", not "private to you".

### Clipping

| What | Cut at |
| --- | --- |
| `text` of a `user_prompt` or `assistant_message` | 32 KiB (32768 bytes) |
| any other string: `output`, `title`, `cwd`, `gitBranch`, and every string inside `input` | 8 KiB (8192 bytes) |

The cut is at a character boundary, and the string ends with the marker
`…[clipped N bytes]` (a single `…` character), where **N is the number of bytes
removed**. The line's `clipped` object then says which field it was and how
long that field was **in bytes as the recorder received it, before redaction**:

```text
"output":"xxxx…[clipped 16384 bytes]","clipped":{"output":24576}
```

`clipped` has the keys `text`, `output`, `input`, `title`, `cwd` and `gitBranch`. For
`input` the number is the length of the input as JSON. Nothing clips a value
before the recorder sees it: the transcript is read from the agent's stored
conversation.


## 5. Which agents have a transcript

A transcript is made from what an agent stored, so what an agent can put in one
is what it stores, in a form Flockdeck can read. This is what was checked in the
code (`internal/session/transcript`):

| Agent | Stores a conversation Flockdeck can read? | What a transcript holds |
| --- | --- | --- |
| **Claude Code** | Yes: `<claude home>/projects/<folder>/<conversation>.jsonl`, under `CLAUDE_CONFIG_DIR` if the agent's catalog entry sets it | Everything in section 3. Gaps below. |
| **Claude API**, **OpenAI API** (and the other built-in chat client agents) | Flockdeck's own chat client keeps its conversations itself, but no transcript can be made from them yet | Nothing: recording and export say so, and write no file. |
| **Codex**, **Gemini CLI**, **Aider**, **opencode**, **Cursor Agent** | No: nothing Flockdeck has a reader for | Nothing: recording and export say so, and write no file. |

Turning recording on for a pane whose agent has no readable conversation is
refused, and says so; a pane that came back recording (from a saved layout or
`spawn --record`) with such an agent stops showing as recording.

For Claude Code the known gaps are:

- **No permission dialogs, status or session lines**: the stored conversation
  has no record of them, so a transcript has no such line type. A tool the user refused has an error `tool_result`;
  one that ran has a result; nothing says a dialog was shown in between.
- **Subagents** (Claude Code's `isSidechain` entries) are left out, and no line says
  which subagent did what. Their result to the main agent is in the main
  agent's tool results.
- **An entry over 8 MiB** (a pasted screenshot, a very large tool result) is
  stepped over and left out, and so is a line that is not JSON. A `tool_call`
  may therefore have no `tool_result`.
- **Thinking** is not recorded.
- **The summary a `/compact` writes** is not recorded; a `conversation_compacted`
  line says where it was made.
- **Usage is per reply, not per line** (see `assistant_message`), covers the main
  agent only, and is the agent's token counts, not a cost.
- **Prompts are only what the user typed**, as described under `user_prompt`.
- **Images** are not in it, in a prompt or in a tool's result: only text blocks
  are, so no image data (base64) is in any line.
- **A conversation Claude Code has deleted** (it removes old ones) cannot be
  exported or recorded from. A recording already made is kept.
- **Limits of following the file.** A stored conversation replaced by a shorter
  one, or found in another folder, is noticed and the transcript is written again
  from the start. One replaced by a file of the same size or longer, whose
  earlier part differs, is not noticed: the follower only knows how far it has
  read. A file that disappears and comes back is read from the start, and the
  transcript is rewritten.

Nothing is claimed here about what the other agents *could* store; if one gains a
reader, this table gets a row.


## 6. Ordering, delivery and crashes

- **Order**: lines are in the order the agent's record has the events, and
  `seq` is the order of writing. Where one message of the agent holds words and
  tool calls, the words come first.
- **Time.** `time` is the event's time as the agent stored it, with one rule that
  makes it monotonic: if an entry has no `timestamp`, an unreadable one, or one
  earlier than the time of the entry before it, the line is given the time of the
  entry before it (the previous entry the writer read, of any kind it reads: a
  prompt, a message, a tool call or result, a compaction). The first entry with no
  usable time is skipped. So `time` never decreases from one line to the next, an
  equal `time` does not mean the events were simultaneous, and a line's `time` is
  not always the agent's own. A `conversation_title` has the time of the entry
  before it (its stored form has none), and `recording_stopped` that of the last
  event.
- **Sizes.** A file is at most 16 MiB (section 1). A string is cut at 8 KiB, or
  32 KiB for a prompt or message (section 4), but a line has no other limit: a
  tool call with many strings in its `input` can be long. Do not read a transcript
  with a fixed line buffer.
- **`seq`** starts at 1 and has no gaps within a file; a missing number means the
  file was edited.
- **A transcript is a function of the stored conversation** alone, its identity
  included (section 3): the same conversation gives the same bytes, however it
  was made, whatever the pane is called or runs. A recording is written as the
  agent's file grows, a piece at a time, and only whole lines are read; the last
  line of the file, if it has no line break yet, is read once it is complete JSON.
  Recording lags the agent by a moment: Flockdeck looks at the agent's record, off
  the goroutines that handle the agent's events, when the agent reports one and
  again shortly after, each look that finds nothing new waiting twice as long as
  the last. After three such looks it stops until the agent reports another event,
  so a quiet conversation costs nothing. If what is stored is replaced by
  something shorter, or turns up in another folder, the transcript is written
  again from the start.
- **Delivery**: nothing is lost between the agent's record and the transcript
  except what section 5 lists. A `tool_call` whose process was killed has no
  `tool_result`; use `toolUseId` to pair them and treat a missing one as normal.
- **Durability**: each line is a single write to the file, with no `fsync`.
  A crash of Flockdeck loses nothing already written, because the operating
  system holds it; a power loss or a kernel crash can lose the most recent lines.
  A file that replaces an earlier one is synced before it is moved into place,
  and on Linux and macOS its folder is synced after, so a power loss leaves the
  earlier file or the new one, not a short one. On Windows the folder is not
  synced (it cannot be opened for that); the file's contents are.
- **How an unclean end shows**: a file whose last line is neither
  `recording_stopped` nor `recording_truncated` was not ended by the writer:
  Flockdeck quit (quitting is not turning recording off, so the pane is
  recording again at the next start, and the file is written again in full,
  complete) or crashed. The last line may be cut off mid-object; ignore it if it
  does not parse.
- **Redaction cannot be undone**: the original text is not kept in the
  transcript. (It is in the agent's own stored conversation, which Flockdeck
  does not change.)

### What is stored and what is worked out

Nearly every field is read from the agent's stored conversation. These are not,
and a consumer that depends on them should know:

| Field | How it is known |
| --- | --- |
| `agent` | Known to Flockdeck: the agent whose conversation was read. |
| `project` | The last element of the first directory the conversation records: a guess at a name from a path, not a stored project name. |
| `session`, `seq` | Made by Flockdeck. |
| `time` | Stored, except where the clamp above applies. |
| `tool` on a `tool_result` | Looked up from the call with the same `toolUseId`. |
| `interrupted` | Read from the words of the stored error. |
| `usage`, `stopReason` | Stored, but written once per reply from the first entry that gives a line, which assumes a reply's entries repeat the same numbers (see *Token usage and stop reason*). |
| `gitBranch`, `cwd`, `agentVersion` on `recording_started`, `recording_stopped`, `recording_truncated` and `conversation_title` | Those of a neighbouring entry, since these lines have no entry of their own. |
| `redacted`, `clipped` | Made by Flockdeck while writing. |

### Compatibility policy

- Within `v: 2`, fields and line types may be **added**; existing ones keep their
  meaning and type. Readers follow the rules in section 2.
- `v` is increased when a field is removed, renamed or changes meaning or type.
  Flockdeck then writes the new version in new files.
- Line types are never reused with a different meaning.
- A new version's release notes say so, and this page and the schema are updated
  in the same change as the code (the build checks the schema against the code).

## 7. Reading a transcript

All of these use [`jq`](https://jqlang.github.io/jq/), and run on one file; on a
whole folder, give `jq` several files.

What was the user asked, and what did the agent say back?

```sh
jq -r 'select(.type == "user_prompt" or .type == "assistant_message")
       | "\(.time)  \(.type | ascii_upcase)\n\(.text)\n"' session.jsonl
```

Every command the agent ran, with how long it took (call to result, paired on `toolUseId`):

```sh
jq -sr '
  (map(select(.type == "tool_call" and .tool == "Bash" and .toolUseId))
     | map({key: .toolUseId, value: .}) | from_entries) as $calls
  | .[] | select(.type == "tool_result" and .toolUseId and $calls[.toolUseId])
  | ($calls[.toolUseId]) as $c
  | "\(((.time | sub("\\.[0-9]+Z$"; "Z") | fromdate) - ($c.time | sub("\\.[0-9]+Z$"; "Z") | fromdate)))s  \($c.input.command)"
' session.jsonl
```

Tokens the conversation used, which is the sum of every `usage` there is (each
reply has one, on its first line; see `assistant_message`). A missing key counts as 0 here only to add; it is unknown, not zero (see *Token usage and stop reason*):

```sh
jq -s '[.[] | select(.usage) | .usage] | {
  input: (map(.inputTokens // 0) | add), output: (map(.outputTokens // 0) | add),
  cacheWritten: (map(.cacheCreationInputTokens // 0) | add),
  cacheRead: (map(.cacheReadInputTokens // 0) | add)}' session.jsonl
```

The conversation's title (the last one), and where it was compacted:

```sh
jq -rs '[.[] | select(.type == "conversation_title")] | last | .title' session.jsonl
jq -c 'select(.type == "conversation_compacted") | {seq, time, trigger, tokensBefore, tokensAfter}' session.jsonl
```

Find lines where something was cut or removed:

```sh
jq -c 'select(.redacted or .clipped) | {seq, type, redacted, clipped}' session.jsonl
```

Read it safely, skipping a line that is not JSON (a last line cut off by a crash)
and any line that is not version 2:

```sh
jq -R 'fromjson? | select(.v == 2)' session.jsonl
```

To skip line types you do not know as well, name the ones you read:

```sh
jq -R 'fromjson? | select(.v == 2 and (.type | IN("user_prompt", "assistant_message", "tool_call", "tool_result")))' session.jsonl
```

In Python:

```python
import json

with open("session.jsonl", encoding="utf-8") as f:
    for line in f:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue  # a line cut off by a crash
        if event.get("v") != 2:
            raise SystemExit("a format version this script does not know")
        if event["type"] == "user_prompt":
            print(event["time"], event["text"])
```


## 8. Recording and exporting give the same file

*Export transcript* in the window (the command palette and the pane's header)
and `flockdeck recordings export <pane-or-conversation-id> [-o file]` write the
conversation an agent has stored, whether or not the pane was ever recorded. They
go through the same writer as a recording, so redaction and clipping (section 4),
the withholding of what a secret file held, and the 16 MiB cap are the same, and
so are the lines: **an export of a conversation is byte for byte what a
recording of it from the start would be**. A test holds the two together by
making one transcript both ways, the recording from a conversation's file as it
grows in awkward pieces.

An export differs from a recording only in where it goes and how it is asked
for:

- **Where.** By default, the `exports` folder of the project's folder (section
  1). **Exporting again replaces the earlier export** of the conversation: the new
  file is written beside it and moved into place only when it is finished and has
  every event the earlier one had, so a failure never loses the earlier one, and
  the window and the command say they replaced it. That is how an export made by
  an older Flockdeck gains what a newer one writes. If the earlier export has an
  event the new one would lack (the stored conversation was cut or changed since;
  events are compared by type, time and tool call id, in order, not by their
  bytes or their place in the file), the earlier file is kept as it was, the
  window says it is not up to date, and the command exits with an error, so that
  a script does not mistake the old file for a fresh one; delete the file to have
  a fresh export. A file that is not a version 2 transcript, or is damaged, is
  always replaced, since it has nothing a new one lacks. With `-o`, a file of
  your own, which
  must not exist, and must not be inside the pane's project or any git
  repository: a transcript can hold secrets, and is never written into a
  project. Files are `0600` on Linux and macOS; on Windows a file inherits the
  permissions of the folder it is written to (for the default, the recordings
  folder: readable by you and by a local administrator).
- **Pane or conversation.** `export` takes a pane's id as the saved layouts hold
  it, or a Claude Code conversation's id. A pane is only a way to find the
  conversation: the lines are the conversation's either way, with nothing of the
  layout's pane name or model in them.
- **Only from the machine itself.** The window's export is refused for a window
  reached through the relay: the file is written on the host, and a path there is
  not something to send to a phone.
- **It says what it did.** How many lines, and how many entries of the
  conversation could not be read; whether it was cut at the size cap.
- **Nothing for an agent with nothing stored.** The command and the window say
  that the agent stores no conversation Flockdeck can read, and write nothing.
- **It can contain secrets.** Redaction is best effort (section 4); the window
  says so every time, and the command after it has run.
- **Finding the file.** *Reveal transcript* (the command palette, and *Show in
  folder* in Pane info) shows the file the pane is recording to, or else its
  newest export, in the file manager, selected; it shows only a file inside the recordings folder,
  and not from a window reached through the relay. `recordings export -reveal`
  shows the file it has just written.

Retention (section 1) does not touch exports, and an export never deletes another
file; an export of the same conversation replaces its earlier export.
