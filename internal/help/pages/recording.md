# Recording a pane

[[action:recordPane]], from the command palette or the record button in a pane
header, saves a structured transcript of that pane's conversation to a file on
this machine, and keeps adding to it as the conversation goes on. It is off for
every pane until you turn it on, and the palette names the action **Stop
recording** while the pane is recording. A recording pane shows a red dot and
the word "Recording" in its header, and its record button is pressed.

Only agent panes can record: a shell has no conversation, so it has no record
button. The first time you turn recording on, Flockdeck asks you to confirm,
and tells you what is kept.

## Exporting without having recorded

[[action:exportTranscript]], from the command palette or the export button in a
pane header, writes the pane's whole conversation as a transcript **whether or
not recording is on**, and says where it put it. It asks every time, because it
writes the whole of a conversation at once: what is written, and that it can
contain secrets. `flockdeck recordings export` does the same from a terminal.
See [the command line](#cli).

A recording and an export of the same conversation are the same lines. Both are
made from the conversation the agent stores itself (for Claude Code, the file
under `~/.claude/projects` that **Resume a past conversation** reads), by one
piece of code. Turning recording on writes the conversation so far, from its
first message, and then follows it; it does not begin at the moment you turned
it on. Export is only from the machine Flockdeck runs on, not from a window
reached through the relay.

**Only agents that store their conversation in a form Flockdeck can read have
anything to record or export.** That is Claude Code today. For Codex, Gemini
CLI, Aider, opencode, Cursor Agent and the built-in chat clients, Flockdeck says
so, and nothing is exported; turning recording on for one is refused, saying so.
A conversation is one file, so only one pane at a time can record it: turning
recording on in a second pane showing the same conversation is refused.

## What is recorded

One JSON object per line, in the order things happened. Every line has a
timestamp (`time`), the conversation's id (`pane`) and a `type`; `conversation`,
`project` and `agent` are there whenever they are known, and left out when
not. A pane's name is not in the
lines, nor is the model it is set to now: they are the pane's, not the
conversation's, and an export has to match a recording of the same conversation.
The one model a line can have is the one that produced that turn, as the agent's
stored conversation records it (`model` on `assistant_message` and `tool_call`
lines, left out where the conversation records none). Transcripts exported
before this was added do not have it until they are exported again:

| `type` | What it holds |
| --- | --- |
| `recording_started`, `recording_stopped` | The first and last line of a transcript |
| `user_prompt` | What you asked (`text`) |
| `assistant_message` | What the agent said (`text`): every message, including the ones between tool calls |
| `tool_call` | The tool (`tool`) and its `input` |
| `tool_result` | What the tool printed (`output`), and `isError` or `interrupted` where it failed or you stopped it |
| `recording_truncated` | The file reached its size cap and ended there |

`time` is when the thing happened, as the agent's own record has it. Every line
also has a `seq` number, counted from 1 in its file, and a line with anything
removed from it says so with `redacted` or `clipped`. The format has a version,
`v`, which goes up if a field ever changes meaning.

The complete field reference, a JSON Schema, ordering and crash behaviour, and
`jq` examples are in
[the transcript format reference](https://github.com/Flockdeck/flockdeck/blob/main/docs/recording-format.md) (`docs/recording-format.md` in
the Flockdeck repository).

A transcript is built from what the agent stored, so it holds what that holds
and nothing else:

- **No permission prompts or their answers, no status changes, no session
  lines.** Claude Code's stored conversation does not record that a dialog was
  shown, how you answered it, or when the pane went idle or waiting. Earlier
  versions of Flockdeck wrote these lines from the agent's live events;
  recordings made then still have them, and the format still defines them.
  Those files are format 1 too: they are told apart by the text of their first
  and last lines (`turned on`, `resumed`, `turned off`, where a transcript made
  now says `start of the transcript` and `end of the transcript`), and in them
  `pane` is the pane's id, not the conversation's.
- **Only what you typed is a prompt.** Entries Claude Code writes for itself
  (a slash command and its output, injected reminders, the note a conversation
  continued from a summary opens with) are left out, and so is a subagent's own
  work. Its result to the main agent is in the main agent's tool results.
- **An entry over 8 MiB** (a pasted screenshot, a huge tool result) cannot be
  read, and is left out. Flockdeck tells you how many.
- **Thinking** is not in it.

## Where it goes

Under Flockdeck's own state directory, never in your project:

```text
<state directory>/recordings/<project>-<hash>/<start time>-<conversation>.jsonl
<state directory>/recordings/<project>-<hash>/exports/<start time>-<conversation>.jsonl
```

There is one file per conversation: `<start time>` is when its first message
was, and a new one starts when the agent goes on in a conversation of its own
(after `/clear`, say). Recording again, or exporting again, makes the same file
again rather than another, so a recording left on across a restart of Flockdeck
catches up from the stored conversation. The new file replaces the earlier one
only if it has every event the earlier one had; if the stored conversation has
been cut or changed so that it does not, the earlier file is kept, Flockdeck says
so, and `flockdeck recordings export` exits with an error. Delete the file to have
a fresh one. The files are readable by you only, and the setting moves with the
pane when you drag it to another tab.

[[action:revealTranscript]], from the command palette or the pane header, opens
your file manager with that pane's transcript file selected: the file it is
recording to if it is recording, otherwise its latest export, and a notice if it
has neither. Like opening the folder it only works from the machine Flockdeck
runs on, and only for a file in the recordings folder. `flockdeck recordings
export -reveal` does it for the file it has just written.

[[action:openRecordings]] opens the folder. `flockdeck recordings` lists the
recordings, newest first (not the exports), and `flockdeck recordings -dir`
prints the folder.
`flockdeck recordings export -o file` writes an export to a path of your
choice, which must not exist and cannot be inside the project or any git
repository. There is no replay view: the files are plain JSON lines, for `jq`
or a script.

### Size and retention

A file stops at 16 MiB: it ends with a `recording_truncated` line, the pane stops
recording and Flockdeck says so. A conversation already longer than that when you
turn recording on is cut at the same place, the same as an export of it, and
recording ends at once. Any one string is cut at 8 KiB (a prompt or
message at 32 KiB), with a marker saying how much was cut. When a recording
starts, the project's folder is tidied: files older than 30 days are deleted, then
the oldest until at most 100 files and 256 MiB are left. **Exports are kept until
you delete them yourself**: they are not tidied, and making one deletes nothing.

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
it as one, is written as it was. In particular it does not catch a secret under
an unconventional name (`SESSION_COOKIE`, `DATABASE_URL`), one that is base64- or
URL-encoded or split across lines, or the contents of a secret file reached
through a symlink or an ordinary-looking name. Treat a transcript as being as
sensitive as the conversation itself, assume a secret may be in it even when no
line is marked redacted, and delete the files you do not want kept. Transcripts
are saved on this machine (user-only on Linux and macOS; on Windows, readable by
you and a local administrator, like any file in your profile) — "on this
machine", not "private to you".

Nothing is sent anywhere: transcripts stay on this machine, and **Open
recordings folder** only works from the machine Flockdeck runs on, not from a
window reached through the relay.

## Starting a helper recorded

An agent can start a helper whose conversation is recorded from the first
message:

```sh
flockdeck spawn -record "refactor the parser"
```

This only works once you have turned recording on yourself, in the window, and
read what it keeps: an agent cannot be the first to switch it on. Without
`-record` a helper is not recorded, even if its parent is.
