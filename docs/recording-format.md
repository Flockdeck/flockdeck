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
while it runs, such as a permission dialog opening, is not in a transcript;
earlier versions of Flockdeck wrote some of those lines, and the format keeps
them so that those files are still readable (see *Lines earlier versions wrote*).

The machine-readable definition is [`recording-line.schema.json`](recording-line.schema.json)
(JSON Schema, draft 2020-12). A test validates lines the recorder really writes
against it, and checks that it names every field and event type the code has,
so this page and the schema are kept level with the code by the build.

Format version: **1**.

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
- `<project>` is the project's name with anything outside `A-Za-z0-9._-` turned
  into `-` (at most 40 characters), and `<hash>` is eight hex digits of a hash of
  the project's directory, so two projects with one name get two folders.
- `<start>` is the time of the conversation's first event, UTC, as
  `20261001T101530Z`, and `<conversation>` is the first eight characters of the
  conversation's id (the pane's id for a conversation that has not been told
  apart from it). That name is the line's `session`.
- A recording is in the project's folder, and an export, unless it was given a
  path of its own, in its `exports` folder.
- The folder is created `0700` and files `0600` (on Windows, with the user's own
  permissions only, as the rest of the state directory).

**One file is one conversation.** It is written from the conversation's first
event: turning recording on for a pane whose conversation is already going writes
what has been said so far, and then follows it. It ends when recording is turned
off, the pane is closed, the size cap is reached, or the agent goes on in a new
conversation (`/clear`), which starts a file of its own. A pane left recording
across a restart of Flockdeck has the file written again from the conversation's
beginning, which gives the same lines and the same name, so it is the same file
and not another. A file is never added to once its transcript has ended, except
by being made again, and a finished transcript is only replaced by one that has
every line it had (it is written beside it and moved into place when finished;
otherwise it is left as it was). If the conversation's first event is now a
different one, so that the name is different, the earlier finished transcript of
the same conversation is removed when the new one is finished: a conversation is
one file. Two panes cannot record one conversation at once, as they would
interleave: the second is refused.

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
delete them by hand**: retention does not count them, and making one deletes
nothing. Nothing else deletes transcripts: delete the files you do not want kept.

## 2. The line format

- JSON Lines: one JSON object per line, separated by `\n`, no blank lines, no
  trailing comma, UTF-8.
- Every line is a complete, self-contained object with the envelope below.
- Each line is written with a single write call.

**How consumers must read it:**

- **Unknown fields: ignore them.** New optional fields may be added in any
  release without changing `v`.
- **Unknown `type` values: skip the line**, and carry on. Do not stop reading.
  Use `seq` to notice that you skipped something, if you care.
- **A different `v`: do not guess.** `v` changes only if a field's meaning or
  type changes, or a field is removed. A reader that knows version 1 should
  refuse, or warn about, a file with another `v`.
- **A last line that is not valid JSON: ignore it.** It is a line cut off by a
  crash (see section 6).

## 3. Fields

### The envelope

Every line has these.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `v` | integer | yes | Format version. `1`. |
| `seq` | integer | yes | The line's number within its file: 1 for the first, then 2, 3, with no gaps. |
| `time` | string | yes | When the event **happened**, as the agent's own record has it: RFC 3339 in UTC with a `Z` and up to nanosecond precision (`2026-10-01T10:15:30.123456789Z`). It never goes back from one line to the next: an entry the agent's record has out of order is given the time of the one before. In files made by earlier versions it is the time Flockdeck wrote the line. |
| `session` | string | yes | The transcript's id: `<start>-<conversation>` (section 1), which is the file's name without `.jsonl` where Flockdeck named it. The same wherever the file is put. |
| `pane` | string | yes | In a transcript made now, the conversation's id: a transcript is of a conversation, which a pane can have several of, and the same conversation must give the same lines whichever pane, layout or command reached it. In files of earlier versions, the pane's id. |
| `paneName` | string | no | **Not written now.** A pane's name changes, and a saved layout can have a stale one, so it cannot be in lines that must be the same however they were made. Files of earlier versions have the pane's name at the time of the line. |
| `project` | string | no | The project's name: the last element of the directory the stored conversation recorded. Absent if it records none. |
| `agent` | string | no | The agent's id (`claude`, `codex`, ...) as `flockdeck agents` lists it. |
| `model` | string | no | **Not written now**, for the reason `paneName` is not: it is the pane's, not the conversation's. Files of earlier versions have the model the pane was asked for. |
| `conversation` | string | no | The agent's own conversation id. It changes when the user runs `/clear`. |
| `type` | string | yes | The event type, one of the types below. |
| `subagent` | string | no | The id of the subagent the event came from. Absent for the main agent. |
| `redacted` | boolean | no | `true` if anything in the line was replaced by redaction (section 4). |
| `clipped` | object | no | For each field that was cut, its length in bytes before cutting (section 4). |

The identity in the envelope (`pane`, `project`, `agent`, `conversation`) is
worked out from the stored conversation alone, in one place, whichever way the
transcript is made: recording, *Export transcript* and the command line all give
the same lines for the same conversation. The pane's name and model, which are
not in the stored conversation, are therefore not in the lines.

### Event types

`Required` below is in addition to the envelope. Every field named is
optional unless it says required.

#### `recording_started`

First line of every file, at the time of the conversation's first event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `start of the transcript`. It says nothing of how the transcript was made, which is the same however it was. Files of earlier versions have `turned on` or `resumed` here. |

```json
{"v":1,"seq":1,"time":"2026-10-01T10:15:30.1Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","project":"shop","agent":"claude","type":"recording_started","text":"start of the transcript"}
```

#### `recording_stopped`

Last line of a file that was ended deliberately, at the time of the last event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `end of the transcript`. Files of earlier versions have `turned off` or `the pane was closed`. |

```json
{"v":1,"seq":41,"time":"2026-10-01T10:31:02.8Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","project":"shop","agent":"claude","type":"recording_stopped","text":"end of the transcript"}
```

#### `recording_truncated`

Last line of a file that reached the size cap. There is no `recording_stopped`
after it.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | What happened, in words. |

```json
{"v":1,"seq":9120,"time":"2026-10-01T11:02:44.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","type":"recording_truncated","text":"the transcript reached its size cap of 16 MiB and ended here"}
```

#### `session`

**No longer written** (see *Lines earlier versions wrote*). The agent's own
session starting or ending (Claude Code's `SessionStart` and `SessionEnd`).

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `start` or `end`. |
| `source` | string | no | On `start`: why it began: `startup`, `resume`, `clear`, `compact` or `fork`. |

```json
{"v":1,"seq":2,"time":"2026-10-01T10:15:31.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","conversation":"c1","type":"session","source":"startup","text":"start"}
```

#### `user_prompt`

What the user typed. The entries Claude Code writes into the conversation for
itself are not prompts and are left out: a slash command and its output, an
injected `<system-reminder>`, a background task's `<task-notification>`, the
note a conversation continued from a summary opens with, "Request interrupted by
user". (Files of earlier versions, written from the agent's live events, hold
some of these, since the agent counts them as prompts.)

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The prompt. Cut at 32 KiB. |

```json
{"v":1,"seq":3,"time":"2026-10-01T10:15:40.2Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"user_prompt","text":"run the tests and fix what fails"}
```

#### `assistant_message`

A message the agent said: **every** text message in the conversation, including
the ones between tool calls ("Let me look at the client first"), in order. A
message that came with tool calls is written before them. (Files of earlier
versions have only the last message of each turn, since that was all the agent
reported while it ran.)

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The message. Cut at 32 KiB. |
| `reason` | string | no | Only in files of earlier versions: `the turn ended on an error`. |

```json
{"v":1,"seq":19,"time":"2026-10-01T10:16:55.9Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"assistant_message","text":"All 212 tests pass."}
```

#### `tool_call`

The agent calling a tool, before it runs.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | yes | The tool's name: `Bash`, `Edit`, `Read`, an MCP tool's full name. |
| `toolUseId` | string | no | The agent's id for this call, to pair it with its `tool_result`. |
| `input` | any JSON | no | The tool's input as the agent gave it, usually an object. Each string in it is redacted and cut at 8 KiB. |

```json
{"v":1,"seq":4,"time":"2026-10-01T10:15:42.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"tool_call","tool":"Bash","toolUseId":"toolu_01","input":{"command":"go test ./...","description":"Run the tests"}}
```

#### `tool_result`

A tool finishing, or failing.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | yes | The tool's name. |
| `toolUseId` | string | no | The id of the call it answers. |
| `output` | string | no | What the tool returned: the text the agent was shown. (The tool's own structured result, which can hold a whole file an edit was made to or an image as base64, is not used.) Cut at 8 KiB. For a failure, the error. |
| `isError` | boolean | no | `true` if the tool failed. |
| `interrupted` | boolean | no | `true` if it failed because the user stopped it (the error says so). Always with `isError`. |

```json
{"v":1,"seq":5,"time":"2026-10-01T10:15:58.4Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"tool_result","tool":"Bash","toolUseId":"toolu_01","output":"ok  \tshop/api\t0.412s"}
```

#### `permission_prompt`

**No longer written.** A permission dialog opening for a tool (Claude Code's
`PermissionRequest`).

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | yes | The tool asking. |
| `toolUseId` | string | no | The call's id, when the agent says it. |
| `input` | any JSON | no | What it wants to do, as for `tool_call`. |

```json
{"v":1,"seq":6,"time":"2026-10-01T10:16:01.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"permission_prompt","tool":"Bash","input":{"command":"rm -rf build"}}
```

#### `permission_outcome`

**No longer written.** How a permission question was answered.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `outcome` | string | yes | `allowed`, `denied`, `abandoned` or `auto_approved`. |
| `inferred` | boolean | no | `true` if the outcome was worked out from what came next rather than reported. |
| `tool` | string | no | The tool the question was about. |
| `toolUseId` | string | no | The call's id, when known. |
| `reason` | string | no | For `auto_approved`, why auto-review let it through. |

There is **no event for the user's answer** to a permission dialog, so for a
prompt the recorder infers it: the tool then ran (`allowed`), it failed or was
interrupted (`denied`), the user submitted a new prompt or the turn ended
without it running (`denied`), or the next permission dialog opened, or the
recording ended, with it still unanswered (`abandoned`). All of those have
`"inferred":true`. `auto_approved` is reported by Flockdeck's auto-review
itself and is not inferred. A `denied` that Claude Code reports through its
`PermissionDenied` event is not inferred either.

```json
{"v":1,"seq":7,"time":"2026-10-01T10:16:03.5Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"permission_outcome","tool":"Bash","outcome":"allowed","inferred":true}
```

#### `status`

**No longer written.** The pane's status changing as a result of an agent event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `status` | string | yes | The new status: `starting`, `working`, `waiting`, `blocked`, `idle`, `exited` or `unknown`. |
| `previous` | string | no | The status before. |
| `detail` | string | no | What the status says it is doing, such as the tool running. |

```json
{"v":1,"seq":8,"time":"2026-10-01T10:16:03.5Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"status","status":"working","previous":"waiting","detail":"Bash"}
```

The agent's stored conversation does not say when a pane went working, waiting
or idle, so a transcript made from it has no `status` lines.

### Lines earlier versions wrote

Versions of Flockdeck before the one that made transcripts from the agent's
stored conversation wrote a recording from the events the agent reported to it
while it ran. Those files are still format 1 and are still valid: they have
`session`, `permission_prompt`, `permission_outcome` and `status` lines, the
last assistant message of each turn only, `time` as the time Flockdeck wrote the
line, and `turned on` / `turned off` / `resumed` in their first and last lines.
The schema and this page keep describing all of them, marked *no longer
written*. Removing the writing of a line type does not change the meaning of any
field or type, which is the only thing that changes `v`, and a reader that
follows section 2 already skips a type it does not see. A consumer should not
rely on any of those four types being present.

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
  `.env.*`, `id_*` that is not `.pub`, `.npmrc`, `.netrc`, `.pgpass`,
  `.git-credentials`, anything with `credential` in its name, and `.pem`, `.key`,
  `.p12`, `.pfx`, `.ppk`, `.keystore` and `.jks` files. The path is a
  `file_path`, `path` or `notebook_path` field, or a word of a Bash `command`.
  The file's name is kept.

A line with any of this has `"redacted": true`.

### What redaction does not catch

Redaction is a pattern list over text, not a guarantee. Treat a transcript as
possibly holding a secret even when no line is marked `redacted`, and do not
record a pane you will handle a secret in if you intend to share the file. In
particular:

- **Unconventional names.** A secret named something not on the list above —
  `SESSION_COOKIE`, `DATABASE_URL`, `SLACK_WEBHOOK`, `dsn`, `db_pass` — with no
  recognisable token shape is kept as written.
- **Encodings and splits.** A secret that is base64- or URL-encoded, or split
  across two fields or two lines, is not recognised: redaction sees the text as
  it is written, and does not decode or reassemble it.
- **Secret files by resolved content.** The secret-file check above is a name
  check only. A symlink inside the project that points at a real secret, a
  secret stored under an ordinary name, or a read whose path sits in a tool
  field other than `file_path`/`path`/`notebook_path`/`command` is not withheld;
  its contents then fall back to pattern redaction, which catches only the
  shapes and names above.
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
| any other string: `output`, `detail`, `reason`, and every string inside `input` | 8 KiB (8192 bytes) |
| a tool `input` that is over 48 KiB once its strings are cut | replaced by `{"_omitted":"too large to record"}` |

The cut is at a character boundary, and the string ends with the marker
`…[clipped N bytes]` (a single `…` character), where **N is the number of bytes
removed**. The line's `clipped` object then says which field it was and how
long that field was **in bytes as the recorder received it, before redaction**:

```json
"output":"xxxx…[clipped 16384 bytes]","clipped":{"output":24576}
```

`clipped` has the keys `text`, `output`, `detail`, `reason` and `input`. For
`input` the number is the length of the input as JSON. Flockdeck's hook also
clips very large values (16 KiB per string; 48 KiB for a message or prompt)
before the recorder sees them, using the same marker: where it did, `clipped`
still gives the original length for `text`, `output`, `detail` and `reason`,
and for `input` it is the length received, so a lower bound.

## 5. Which agents have a transcript

A transcript is made from what an agent stored, so what an agent can put in one
is what it stores, in a form Flockdeck can read. This is what was checked in the
code (`internal/session/transcript`):

| Agent | Stores a conversation Flockdeck can read? | What a transcript holds |
| --- | --- | --- |
| **Claude Code** | Yes: `<claude home>/projects/<folder>/<conversation>.jsonl`, under `CLAUDE_CONFIG_DIR` if the agent's catalog entry sets it | Everything in section 3 that is not marked *no longer written*. Gaps below. |
| **Claude API**, **OpenAI API** (and the other built-in chat client agents) | Flockdeck's own chat client keeps its conversations itself, but no transcript can be made from them yet | Nothing: recording and export say so, and write no file. |
| **Codex**, **Gemini CLI**, **Aider**, **opencode**, **Cursor Agent** | No: nothing Flockdeck has a reader for | Nothing: recording and export say so, and write no file. |

Turning recording on for a pane whose agent has no readable conversation is
refused, and says so; a pane that came back recording (from a saved layout or
`spawn --record`) with such an agent stops showing as recording.

For Claude Code the known gaps are:

- **No permission dialogs, status or session lines**: the stored conversation
  has no record of them. A tool the user refused has an error `tool_result`;
  one that ran has a result; nothing says a dialog was shown in between.
- **Subagents** (Claude Code's `isSidechain` entries) are left out, so there are
  no lines with `subagent` set. Their result to the main agent is in the main
  agent's tool results.
- **An entry over 8 MiB** (a pasted screenshot, a very large tool result) is
  stepped over and left out, and so is a line that is not JSON. A `tool_call`
  may therefore have no `tool_result`.
- **Thinking** is not recorded.
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
  `seq` is the order of writing. `time` does not go back (section 3). Where
  one message of the agent holds words and tool calls, the words come first.
- **A transcript is a function of the stored conversation** alone, its identity
  included (section 3): the same conversation gives the same bytes, however it
  was made, whatever the pane is called or runs. A recording is written
  as the agent's file grows, a piece at a time, and only whole lines are read; the
  last line of the file, if it has no line break yet, is read once it is
  complete JSON. Recording lags the agent by a moment: Flockdeck looks at the
  agent's record, off the goroutines that handle the agent's events, when the
  agent reports one and again shortly after, each look that finds nothing new
  waiting twice as long as the last. After three such looks it stops until the
  agent reports another event, so a quiet conversation costs nothing. If what is
  stored is replaced by something shorter, or turns up in another folder, the
  transcript is written again from the start.
- **Delivery**: nothing is lost between the agent's record and the transcript
  except what section 5 lists. A `tool_call` whose process was killed has no
  `tool_result`; use `toolUseId` to pair them and treat a missing one as normal.
- **No gaps in `seq`** within a file: a missing number means the file was edited.
- **Durability**: each line is a single write to the file, with no `fsync`.
  A crash of Flockdeck loses nothing already written, because the operating
  system holds it; a power loss or a kernel crash can lose the most recent lines.
- **How an unclean end shows**: a file whose last line is neither
  `recording_stopped` nor `recording_truncated` was not ended by the writer:
  Flockdeck quit (quitting is not turning recording off, so the pane is
  recording again at the next start, and the file is written again in full,
  complete) or crashed. The last line may be cut off mid-object; ignore it if it
  does not parse.
- **Redaction cannot be undone**: the original text is not kept in the
  transcript. (It is in the agent's own stored conversation, which Flockdeck
  does not change.)

### Compatibility policy

- Within `v: 1`, fields and event types may be **added**; existing ones keep their
  meaning and type. Readers follow the rules in section 2.
- `v` is increased for a change of how lines are separated, or a field removed,
  renamed or changed in type, where the change is not one of the amendments
  below. Flockdeck then writes the new version in new files.
- Event types are never reused with a different meaning.
- A new version's release notes say so, and this page and the schema are updated in
  the same change as the code (the build checks the schema against the code).

**What changed within version 1, and what that means for a reader.** The release
that made transcripts from the agent's stored conversation (section 8) changed
some things while staying at `v: 1`. That is a departure from the policy above,
which would have increased `v` for a field that changes meaning, and it was
decided on purpose: the shape of a line is the same, and a reader written for the
files of Flockdeck 0.3.47 reads the new ones, but it can no longer assume what it
could. So **files of 0.3.47 and files of later versions share `v: 1` and differ
in these ways**:

| | 0.3.47 | Later |
| --- | --- | --- |
| `pane` | the pane's id | the conversation's id |
| `time` | when Flockdeck wrote the line | when the event happened, from the agent's record |
| `paneName`, `model` | written | not written |
| `recording_started` / `recording_stopped` text | `turned on` or `resumed` / `turned off` or `the pane was closed` | `start of the transcript` / `end of the transcript` |
| `session`, `permission_prompt`, `permission_outcome`, `status` | written | not written |
| `assistant_message` | the last message of each turn | every message |
| `user_prompt` | includes what the agent counts as prompts but a person did not type | only what the person typed |
| `output` of a `tool_result` | the hook's result text | the text the agent was shown |
| file name | `<start of recording>-<pane>.jsonl` | `<first event>-<conversation>.jsonl` |

**How to tell them apart.** The reliable sign is the first line: a
`recording_started` whose `text` is `start of the transcript` is a later file, and
`turned on` or `resumed` is a 0.3.47 one. A file that has any `status`, `session`,
`permission_prompt` or `permission_outcome` line, or a `paneName`, is a 0.3.47
one (a later file has none). The file name cannot be told apart by its shape, as
both are a time and eight characters. There is no field that says; a consumer
that needs one reading should key on the first line.

Stopping writing a line type is not itself a change to `v`: its definition stays
here and in the schema, marked *no longer written*, so files already made are
still described.

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

Permission prompts and what happened to them (only in files made by earlier
versions; a current transcript has none):

```sh
jq -r 'select(.type == "permission_outcome")
       | "\(.time)  \(.tool)  \(.outcome)\(if .inferred then " (inferred)" else "" end)"' session.jsonl
```

How long the pane spent in each status, in order (earlier versions' files only):

```sh
jq -r 'select(.type == "status") | "\(.time)  \(.previous // "-") -> \(.status)"' session.jsonl
```

Find lines where something was cut or removed:

```sh
jq -c 'select(.redacted or .clipped) | {seq, type, redacted, clipped}' session.jsonl
```

Read it safely, skipping a last line cut off by a crash and any type you do not know:

```sh
jq -R 'fromjson? | select(.v == 1)' session.jsonl
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
        if event.get("v") != 1:
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
recording of it from the start would be**, given the same pane name, project,
agent and model. A test holds the two together by making one transcript both
ways, the recording from a conversation's file as it grows in awkward pieces.

An export differs from a recording only in where it goes and how it is asked
for:

- **Where.** By default, the `exports` folder of the project's folder (section
  1); made again if it is exported again. With `-o`, a file of your own, which
  must not exist, and must not be inside the pane's project or any git
  repository: a transcript can hold secrets, and is never written into a
  project. Files are `0600`.
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

Retention (section 1) does not touch exports and an export deletes nothing.
