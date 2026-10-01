# Recording a pane

[[action:recordPane]], from the command palette or the record button in a pane
header, saves a structured transcript of that pane's agent interaction to a
file on this machine. It is off for every pane until you turn it on, and the
palette names the action **Stop recording** while the pane is recording. A
recording pane shows a red dot and the word "Recording" in its header, and its
record button is pressed.

Only agent panes can record: a shell reports no events, so it has no record
button. The first time you turn recording on, Flockdeck asks you to confirm,
and tells you what is kept.

## What is recorded

One JSON object per line, in the order things happened. Every line has a
timestamp (`time`), the pane's id and name (`pane`, `paneName`), the `project`,
the `agent` and `model`, and a `type`:

| `type` | What it holds |
| --- | --- |
| `recording_started`, `recording_stopped` | The first and last line of a session, with why |
| `session` | The agent's session starting or ending |
| `user_prompt` | What you asked (`text`) |
| `assistant_message` | The agent's message at the end of a turn (`text`) |
| `tool_call` | The tool (`tool`) and its `input` |
| `tool_result` | What the tool printed (`output`), and `isError` or `interrupted` where it failed or you stopped it |
| `permission_prompt` | A permission dialog opening, for a tool and its `input` |
| `permission_outcome` | How it was answered: `allowed`, `denied`, or `auto_approved` by auto-review, with `inferred: true` where it was worked out from what happened next |
| `status` | The pane's status changing, with the `previous` one |
| `recording_truncated` | The file reached its size cap and the recording ended |

A tool call made by a subagent has a `subagent` field naming it. Every line also
has a `seq` number, counted from 1 in its file, and a line with anything
removed from it says so with `redacted` or `clipped`. The format has a version,
`v`, which goes up if a field ever changes meaning.

The complete field reference, a JSON Schema, what each agent can report,
ordering and crash behaviour, and `jq` examples are in
[the transcript format reference](https://github.com/Flockdeck/flockdeck/blob/main/docs/recording-format.md) (`docs/recording-format.md` in
the Flockdeck repository).

Everything here comes from the events the agent reports to Flockdeck, not from
what the terminal shows. That leaves some gaps, which are Claude Code's to
close:

- **Assistant messages** are only the last one of each turn. Claude Code reports
  no event for the messages it writes between tool calls, and a Claude Code too
  old to include the final message in its stop event gives none at all.
- **Permission outcomes are inferred.** There is no event for your answer to a
  permission prompt, so it is read from what came next: the tool ran
  (`allowed`), or the turn moved on without it (`denied`).
- **Status changes** are the ones agent events cause. A pane going quiet, or its
  process ending, is not recorded as one.
- **Agents that report no events** record only what they do report. The built-in
  chat client gives session, prompt, status and tool-name lines but no tool
  inputs, results or messages, and Codex, Gemini CLI, Aider, opencode and Cursor
  Agent report nothing, so their recordings hold only the start and the end.

## Where it goes

Under Flockdeck's own state directory, never in your project:

```text
<state directory>/recordings/<project>-<hash>/<start time>-<pane>.jsonl
```

There is one file per recording session, from turning recording on to turning
it off, closing the pane, or a restart of Flockdeck while it is on. The files are
readable by you only. A pane left recording is still recording after a restart,
in a new file, and the setting moves with the pane when you drag it to another
tab.

[[action:openRecordings]] opens the folder. `flockdeck recordings` lists the
recordings, newest first, and `flockdeck recordings -dir` prints the folder.
There is no replay view: the files are plain JSON lines, for `jq` or a script.

### Size and retention

A file stops at 16 MiB: it ends with a `recording_truncated` line, the pane stops
recording and Flockdeck says so. Any one string is cut at 8 KiB (a prompt or
message at 32 KiB), with a marker saying how much was cut. When a recording
starts, the project's folder is tidied: files older than 30 days are deleted, then
the oldest until at most 100 files and 256 MiB are left.

## Secrets

A transcript can contain secrets: they are in the prompts you type, the files an
agent reads and the commands it runs. Flockdeck removes what it can recognise:

- private key blocks, common token shapes (Anthropic, OpenAI, GitHub, GitLab,
  Slack, AWS and Google keys, JWTs) and `Authorization` credentials;
- passwords inside URLs;
- the value of anything named like a secret: `API_KEY=...`, `password: ...`,
  `"client_secret": ...`;
- the output of reading a file named like a secret (`.env`, private keys,
  `credentials` files), and what is written to one.

**This is best effort.** A secret in an unusual shape, in text that does not name
it as one, is recorded as it was. In particular it does not catch a secret under
an unconventional name (`SESSION_COOKIE`, `DATABASE_URL`), one that is base64- or
URL-encoded or split across lines, or the contents of a secret file reached
through a symlink or an ordinary-looking name. Treat a recording as being as
sensitive as the conversation itself, assume a secret may be in it even when no
line is marked redacted, and delete the files you do not want kept. Transcripts
are saved on this machine (user-only on Linux and macOS; on Windows, readable by
you and a local administrator, like any file in your profile) — "on this
machine", not "private to you".

Nothing is sent anywhere: recordings stay on this machine, and `Open recordings
folder` only works from the machine Flockdeck runs on, not from a phone.

## Starting a helper recorded

An agent can start a helper whose interaction is recorded from the first
message:

```sh
flockdeck spawn --record "refactor the parser"
```

This only works once you have turned recording on yourself, in the window, and
read what it keeps: an agent cannot be the first to switch it on. Without
`--record` a helper is not recorded, even if its parent is.
