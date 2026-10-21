# Stream recording: line format reference

A stream recording is the JSON-RPC traffic between Flockdeck and a structured
agent (one that speaks the Agent Client Protocol), written as it happens. It is
not the transcript described in [recording-format.md](recording-format.md), which
is made from the conversation an agent stored. The two sit in the same folder
and are kept under the same rules, but they are different files with different
lines. There is no player for stream recordings yet.

The definition is [`rpc-recording-line.schema.json`](rpc-recording-line.schema.json)
(JSON Schema, draft 2020-12). Tests check every line the recorder writes against
it, and check that this page and the schema name the same fields.

Format version: **1**.

## 1. Where the files are

A stream recording is in the project's folder under `recordings/`, beside the
terminal recordings, and its name ends in `.rpc.jsonl`:

```
recordings/<project>-<hash>/20261019T120000Z-conv1234.rpc.jsonl
```

It is made readable by its owner only. It is counted with the terminal
recordings in the folder by the same retention: files older than 30 days go,
then the oldest until at most 100 files and 256 MiB are left. A recording that
is still being written, of either kind, is never the one removed. The
recordings window does not offer a stream recording, because its first line is
not the start of a transcript; `flockdeck recordings` lists the file with no
project or conversation.

Whether a stream recording is made is decided by whoever calls
`Manager.StartRPC`, not by the recorder. A pane with recording off never calls
it, and a relay window cannot turn it on: recording is a setting of the desktop
app for a pane, like the terminal one. `StartRPC` opens nothing once the
`Manager` is closed, and closing the `Manager` closes the recordings it opened.

## 2. Sensitive content

The lines hold prompts, file contents and tool output, so the file is as
sensitive as a terminal recording. Before a line is written:

- Every string goes through the same redaction as a transcript, and a value
  under a key that names a secret (`apiKey`, `token`, `password`...) is replaced
  whole. So is a key that holds a secret: it is written as `[redacted]`
  (`[redacted]#2` for a second one in the same object). Keys over 256 bytes are
  cut. Redaction is best effort: see `record.Redact`.
- A secret can be split between two chunks of text. The end of the text of a
  line (its last two words, up to 256 bytes) is therefore not written until the
  text that follows it arrives, and is redacted together with it, whether the
  line was written in between by the timer, because it was full, or because
  another message came in the middle. The end is written at the latest when the
  recording closes. The result is that the last words of a run of text can be
  written after later messages, so a reader can see the end of a reply after a
  message of the next turn; each line's `time` is when it was seen, and `seq`
  is the order written. A key that never gets finished cannot be recognised, and
  what there was of it is written when the recording closes.
- A label can already be written when its value arrives: "the password:" in one
  line and the password in the text after it. The recorder therefore keeps the
  last 16 KiB of the text it has written, for each kind of text, and redacts
  what follows together with it, so that the label, or the first lines of a
  private key, are seen. Only the new part is written. A label or the start of
  a key further back than 16 KiB is not seen, with one exception: a private key
  that has no END yet keeps a BEGIN line in the context, so the rest of it is
  redacted. A key is 1.7 to 3.4 KB, so the line is held for 64 KiB of text
  after the BEGIN and then let go. A BEGIN line that is not a key,
  such as one quoted in prose, therefore hides the next 64 KiB of that kind of
  text and no more. When more than 16 kinds of text are open at once the context
  of all of them is let go.
- Every string is cut at 8 KiB, except the text of a prompt and of a message or
  thought chunk, which is cut at 32 KiB. A cut string ends in a marker saying
  how many bytes went.
- A caller can set a hook that sees each message after all that and may clip
  fields, or drop the message (`record.RPCHook`).
  A hook that panics drops the message it was given. At close that can be the
  held end of a reply, which is then not in the file; the other lines and the
  closing line are still written.

A line of traffic longer than 1 MiB, or with more than 50,000 array items and
object members, is not parsed, because parsing costs about a hundred times its
length in memory; a line is written with a `text` saying how long it was and
nothing of its content. A message that is over a quarter of the size cap once
redacted and clipped is replaced the same way.
once redacted and clipped is replaced the same way.

## 3. Lines

One JSON object per line, in the order the traffic was seen.

| `type` | When |
| --- | --- |
| `rpc_started` | First line. `project`, `agent`, `conversation` and a `text`. |
| `rpc_message` | One message, in `msg`, with `dir` `in` (from the agent) or `out` (to it). |
| `rpc_dropped` | Second line, only once the cap has dropped old lines. `dropped` counts them. |
| `rpc_gap` | Anywhere after the first line, where lines of traffic were never recorded. `dropped` counts them. |
| `rpc_stopped` | Last line, when the recording is closed. A file without one was cut short. |

Every line has `v`, `seq`, `time`, `session` and `type`. `seq` rises on every
line. After lines are dropped it has a gap, and the `rpc_dropped` line's `seq` is
that of the last line dropped.

Other fields, where they apply:

- `msg`: the JSON-RPC message, redacted and clipped.
- `raw`: for a line of traffic that was not a JSON object, redacted and cut at
  8 KiB, in place of `msg`.
- `redacted`: `true` when redaction replaced something in the line.
- `clipped`: how many strings were cut, or 1 for a message that was not kept.
- `text`: what a start, drop or stop line is, or what became of a message that
  was too long.
- `merged` and `until`: see section 4.

Example, a message stream broken into three lines (`msg` shortened):

```json
{"v":1,"seq":1,"time":"2026-10-19T12:00:00.001Z","session":"20261019T120000Z-conv1234.rpc","type":"rpc_started","text":"start of the stream recording","project":"demo","agent":"opencode"}
{"v":1,"seq":2,"time":"2026-10-19T12:00:00.100Z","session":"20261019T120000Z-conv1234.rpc","type":"rpc_message","dir":"out","msg":{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s1","prompt":[{"type":"text","text":"hello"}]}}}
{"v":1,"seq":3,"time":"2026-10-19T12:00:00.300Z","until":"2026-10-19T12:00:00.420Z","merged":4,"session":"20261019T120000Z-conv1234.rpc","type":"rpc_message","dir":"in","msg":{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello there"}}}}}
```

A `rpc_gap` line is written when the client that taps the traffic had to drop
lines it could not hand over in time (`acp.Client.TapDropped`), by
`RPCRecorder.Gap`. It sits at the place in the stream where they were lost, has
its own `seq`, and carries `dropped`, the number of lines, and a `text`. It is
not a `rpc_dropped` line: those say the cap removed old lines from the file,
this says lines never reached it, and a file can have any number of them. A
reader gets it like any other line; it does not skip it, and a player should
show a break there. Nothing is written for a count of zero or less.

## 4. Coalescing

Streams repeat themselves: an agent may send a hundred one-word chunks of a
reply, or rewrite a tool call's whole output twice a second. The recorder
joins neighbours the way the transcript bridge does:

- Consecutive `agent_message_chunk`, `agent_thought_chunk` or
  `user_message_chunk` updates of one session become one line whose text is
  the parts joined. A line holds at most 32 KiB of text; the next part starts a
  new line.
- Consecutive `tool_call_update` messages for one tool call become one line;
  where they carry the same field the later one wins.
- Any other message ends the run, so the order of the stream is kept. A held line
  is written at the latest 250 ms after its first part arrived.

A joined line has `merged` (how many messages it stands for) and `until` (when
the last one was seen); `time` is the first. Nothing else is joined. Apart from the held end of a run of
text (section 2), a message is never reordered.

## 5. The size cap

A file is capped at 16 MiB. When the next line would pass the cap, the file is
rewritten with its first line, a `rpc_dropped` line, and the newest lines that
fit in half the cap, and recording goes on. The oldest lines are the ones lost.
If the file cannot be rewritten, recording stops and the error is returned from
`Close`.

## 6. Reading a file

`record.ReadRPCFile` reads a file and returns its lines; `record.ScanRPC` calls
a function for each line instead, for a caller that should not hold the file.
Both stop with `ErrRPCTooLarge` at 32 MiB of input, which is twice what a
recording can reach. A line that is not a JSON object, or is over 8 MiB, is
skipped and counted in `Damaged`. A last line with no newline is what a crash
leaves: it is kept if it is whole JSON, skipped if it is not, and `TornTail` is
set either way.
