# Pane recording: transcript format reference

This is the reference for the files Flockdeck writes when a pane's **Record**
toggle is on. For turning it on and what it is for, see *Recording a pane* in
the in-app help (`F1`) or the README. This page is about the files.

The machine-readable definition is [`recording-line.schema.json`](recording-line.schema.json)
(JSON Schema, draft 2020-12). A test validates lines the recorder really writes
against it, and checks that it names every field and event type the code has,
so this page and the schema are kept level with the code by the build.

Format version: **1**.

## 1. Where the files are

```text
<state directory>/recordings/<project>-<hash>/<start>-<pane>.jsonl
```

- `<state directory>` is Flockdeck's per-user directory: `%AppData%\flockdeck`
  on Windows, `~/Library/Application Support/flockdeck` on macOS,
  `$XDG_CONFIG_HOME/flockdeck` (default `~/.config/flockdeck`) on Linux.
  `flockdeck recordings -dir` prints the `recordings` folder. Nothing is ever
  written into a project's repository.
- `<project>` is the project's name with anything outside `A-Za-z0-9._-` turned
  into `-` (at most 40 characters), and `<hash>` is eight hex digits of a hash of
  the project's directory, so two projects with one name get two folders.
- `<start>` is the session's start, UTC, as `20261001T101530Z`, and `<pane>` is the
  first eight characters of the pane's id. If that name is taken (recording
  switched off and on within one second) `-2`, `-3` and so on is added.
- The folder is created `0700` and files `0600` (on Windows, with the user's own
  permissions only, as the rest of the state directory).

**One file is one recording session**: from recording being turned on (or, for
a pane left recording across a restart of Flockdeck, from its first event
after the restart) until it is turned off, the pane is closed, the size cap is
reached, or Flockdeck quits. A new session is a new file; files are never
appended to after the session that wrote them ends.

**There is no rotation.** A session file has a size cap of **16 MiB**. When the
next line would pass it, the recorder writes one `recording_truncated` line, the
file is closed, and the pane's recording is switched off (the window says so).
Turning recording on again starts a new file.

**Retention** is applied to a project's folder each time a session starts in it:
files with a modification time older than **30 days** are deleted; then, if more
than **100** files or **256 MiB** remain, the oldest are deleted until neither is
exceeded. The file just opened is never deleted, and files in the folder that
do not end in `.jsonl` are left alone. Nothing else deletes recordings:
delete the files you do not want kept.

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
| `time` | string | yes | When Flockdeck **wrote** the line, RFC 3339 in UTC with a `Z` and up to nanosecond precision (`2026-10-01T10:15:30.123456789Z`). It is the time the event reached Flockdeck, not the agent's own clock. |
| `session` | string | yes | The session: the file's name without `.jsonl`. |
| `pane` | string | yes | The pane's id, which is stable across restarts of the pane and of Flockdeck. |
| `paneName` | string | no | The pane's display name at the time of the line. |
| `project` | string | no | The project's name (the last element of its directory). |
| `agent` | string | no | The agent's id (`claude`, `codex`, ...) as `flockdeck agents` lists it. |
| `model` | string | no | The model the pane was asked for. Absent when it runs whatever the agent defaults to. |
| `conversation` | string | no | The agent's own conversation id. It changes when the user runs `/clear`. |
| `type` | string | yes | The event type, one of the types below. |
| `subagent` | string | no | The id of the subagent the event came from. Absent for the main agent. |
| `redacted` | boolean | no | `true` if anything in the line was replaced by redaction (section 4). |
| `clipped` | object | no | For each field that was cut, its length in bytes before cutting (section 4). |

`agent`, `model` and `paneName` are read when the line is written, so a pane
renamed or switched mid-session changes from that line on.

### Event types

`Required` below is in addition to the envelope. Every field named is
optional unless it says required.

#### `recording_started`

First line of every file.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | Why: `turned on`, or `resumed` for a pane that was left recording when Flockdeck last quit. |

```json
{"v":1,"seq":1,"time":"2026-10-01T10:15:30.1Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","paneName":"api","project":"shop","agent":"claude","model":"opus","type":"recording_started","text":"turned on"}
```

#### `recording_stopped`

Last line of a file that was ended deliberately.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `turned off` or `the pane was closed`. |

```json
{"v":1,"seq":41,"time":"2026-10-01T10:31:02.8Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","paneName":"api","project":"shop","agent":"claude","model":"opus","type":"recording_stopped","text":"turned off"}
```

#### `recording_truncated`

Last line of a file that reached the size cap. There is no `recording_stopped`
after it.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | What happened, in words. |

```json
{"v":1,"seq":9120,"time":"2026-10-01T11:02:44.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","type":"recording_truncated","text":"the recording reached its size cap of 16 MiB and ended here"}
```

#### `session`

The agent's own session starting or ending (Claude Code's `SessionStart` and
`SessionEnd`).

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | `start` or `end`. |
| `source` | string | no | On `start`: why it began: `startup`, `resume`, `clear`, `compact` or `fork`. |

```json
{"v":1,"seq":2,"time":"2026-10-01T10:15:31.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","conversation":"c1","type":"session","source":"startup","text":"start"}
```

#### `user_prompt`

What the user submitted. A message Claude Code submits on the user's behalf,
such as a `<task-notification>` about a finished background task, is recorded
the same way: it is a prompt as far as the agent can tell.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The prompt. Cut at 32 KiB. |

```json
{"v":1,"seq":3,"time":"2026-10-01T10:15:40.2Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"user_prompt","text":"run the tests and fix what fails"}
```

#### `assistant_message`

The agent's message at the end of a turn (a `Stop`, `SubagentStop` or
`StopFailure` event that carries one). It is **not** every message the agent
wrote: see section 5.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `text` | string | yes | The message. Cut at 32 KiB. |
| `reason` | string | no | `the turn ended on an error` where the event was a `StopFailure`. |

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
| `output` | string | no | What the tool returned. A structured response is written as compact JSON text. Cut at 8 KiB. For a failure, the error. |
| `isError` | boolean | no | `true` if the tool failed. |
| `interrupted` | boolean | no | `true` if it failed because the user stopped it. Always with `isError`. |

```json
{"v":1,"seq":5,"time":"2026-10-01T10:15:58.4Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"tool_result","tool":"Bash","toolUseId":"toolu_01","output":"ok  \tshop/api\t0.412s"}
```

#### `permission_prompt`

A permission dialog opening for a tool (Claude Code's `PermissionRequest`).

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `tool` | string | yes | The tool asking. |
| `toolUseId` | string | no | The call's id, when the agent says it. |
| `input` | any JSON | no | What it wants to do, as for `tool_call`. |

```json
{"v":1,"seq":6,"time":"2026-10-01T10:16:01.0Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"permission_prompt","tool":"Bash","input":{"command":"rm -rf build"}}
```

#### `permission_outcome`

How a permission question was answered.

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

The pane's status changing as a result of an agent event.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `status` | string | yes | The new status: `starting`, `working`, `waiting`, `blocked`, `idle`, `exited` or `unknown`. |
| `previous` | string | no | The status before. |
| `detail` | string | no | What the status says it is doing, such as the tool running. |

```json
{"v":1,"seq":8,"time":"2026-10-01T10:16:03.5Z","session":"20261001T101530Z-0123abcd","pane":"0123abcd-5e6f-4a7b-8c9d-0e1f2a3b4c5d","agent":"claude","type":"status","status":"working","previous":"waiting","detail":"Bash"}
```

Only changes that an agent event causes are recorded. A pane going quiet on a
timer, or its process ending, changes the status without a line.

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

## 5. What each agent reports

The recorder is fed by the events an agent reports to Flockdeck (`internal/hooks`),
not by reading the screen. What an agent can put in a recording is therefore what
it reports, and this is what was checked in the code:

| Agent | Reports events? | What a recording holds |
| --- | --- | --- |
| **Claude Code** | Yes, by command hooks | Everything in section 3. Gaps below. |
| **Claude API**, **OpenAI API** (and the other built-in chat client agents: Gemini API, OpenAI-compatible endpoints) | Yes: Flockdeck's own chat client reports through the same hook protocol | `session`, `user_prompt` (text), `tool_call` and `tool_result` with the tool's **name only** (no input, output or ids), `status`. When a tool asks permission it reports a notification, which is not recorded, so there are no `permission_*` lines. No `assistant_message`: its stop event carries no message. |
| **Codex**, **Gemini CLI**, **Aider**, **opencode**, **Cursor Agent** | No: Flockdeck has no hook for them, and infers their status from the terminal | `recording_started` and `recording_stopped` only. Their status is read off the screen, not from events, so it is not recorded either. |

For Claude Code the known gaps are:

- **Only the final assistant message of a turn.** The messages written between
  tool calls have no hook. A Claude Code that predates `last_assistant_message` on its
  stop event gives none.
- **Permission outcomes are inferred**, as described under `permission_outcome`.
- **A `tool_result` may be missing** for a call whose process was killed before
  its hook could report.
- **Subagents**: a subagent's tool calls and its final message are recorded
  with `subagent` set; its own prompt and the messages in between are not.
- **Notifications** (the idle nudge, "Claude needs your attention") are not
  recorded as such; they show up as `status` changes.
- **Thinking** is not reported and is not recorded.

Nothing is claimed here about what the other agents *could* report with a
different integration; if one gains hooks, this table gets a row.

## 6. Ordering, delivery and crashes

- **Order**: lines are in the order Flockdeck handled the events, and `seq`
  is the order of writing. Hooks run as separate short-lived processes, so two
  events that fire within a few milliseconds of each other can arrive in the
  opposite order to the one the agent fired them in. `time` is when
  Flockdeck wrote the line.
- **Delivery is best effort and at most once.** An event whose hook could not
  reach Flockdeck (it gives up after three seconds) is not recorded and
  nothing marks the gap, so a `tool_call` may have no `tool_result`. Use
  `toolUseId` to pair them and treat a missing one as normal.
- **No gaps in `seq`** within a file: a missing number means the file was edited.
- **Durability**: each line is a single write to the file, with no `fsync`.
  A crash of Flockdeck loses nothing already written, because the operating
  system holds it; a power loss or a kernel crash can lose the most recent lines.
- **How an unclean end shows**: a file whose last line is neither
  `recording_stopped` nor `recording_truncated` was not ended by the recorder:
  Flockdeck quit (quitting is not turning recording off, so the pane is
  recording again at the next start, in a new file) or crashed. The last line
  may be cut off mid-object; ignore it if it does not parse. The pane's
  recording continues in a *new* file, whose `recording_started` says `resumed`.
- **Redaction cannot be undone**: the original text is not kept anywhere.

### Compatibility policy

- Within `v: 1`, fields and event types may be **added**; existing ones keep their
  meaning and type. Readers follow the rules in section 2.
- `v` is increased for anything else (a field removed, renamed or changed in
  meaning or type, or a change in how lines are separated). Flockdeck then
  writes the new version in new files; **it never rewrites old files**, so files of
  both versions can sit in one folder.
- Event types are never reused with a different meaning.
- A new version's release notes say so, and this page and the schema are updated in
  the same change as the code (the build checks the schema against the code).

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

Permission prompts and what happened to them:

```sh
jq -r 'select(.type == "permission_outcome")
       | "\(.time)  \(.tool)  \(.outcome)\(if .inferred then " (inferred)" else "" end)"' session.jsonl
```

How long the pane spent in each status, in order:

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
