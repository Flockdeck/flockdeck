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
then the oldest until at most 100 files and 256 MiB are left. The recordings window does not offer a stream recording, because its
first line is not the start of a transcript; `flockdeck recordings` lists the
file with no project or conversation.

## 2. Sensitive content

The lines hold prompts, file contents and tool output, so the file is as
sensitive as a terminal recording. Before a line is written:

- Every string goes through the same redaction as a transcript, and a value
  under a key that names a secret (`apiKey`, `token`, `password`...) is replaced
  whole. Redaction runs on a coalesced message after its parts are joined, so a
  secret split across two chunks is still found. It is best effort: see
  `record.Redact`.
- Every string is cut at 8 KiB, except the text of a prompt and of a message or
  thought chunk, which is cut at 32 KiB. A cut string ends in a marker saying
  how many bytes went.
- A caller can set a hook that sees each message after both and may clip fields,
  or drop the message (`record.RPCHook`).

A line of traffic longer than 8 MiB is not parsed; a line is written with a
`text` saying how long it was. A message that is over a quarter of the size cap
once redacted and clipped is replaced the same way.

## 3. Lines

One JSON object per line, in the order the traffic was seen.

| `type` | When |
| --- | --- |
| `rpc_started` | First line. `project`, `agent`, `conversation` and a `text`. |
| `rpc_message` | One message, in `msg`, with `dir` `in` (from the agent) or `out` (to it). |
| `rpc_dropped` | Second line, only once the cap has dropped old lines. `dropped` counts them. |
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
the last one was seen); `time` is the first. Nothing else is joined, and a
message is never reordered.

## 5. The size cap

A file is capped at 16 MiB. When the next line would pass the cap, the file is
rewritten with its first line, a `rpc_dropped` line, and the newest lines that
fit in half the cap, and recording goes on. The oldest lines are the ones lost.
If the file cannot be rewritten, recording stops and the error is returned from
`Close`.

## 6. Reading a file

`record.ReadRPCFile` reads a file and returns its lines. A line that is not a
JSON object, or is over 32 MiB, is skipped and counted in `Damaged`. A last line
with no newline is what a crash leaves: it is kept if it is whole JSON, skipped
if it is not, and `TornTail` is set either way.
