# opencode end to end (T65)

This is the record of one run of a real opencode behind a structured pane, driven only through the
control commands a window uses. The test is `TestRealOpencodeEndToEnd` in
`internal/server/opencode_e2e_test.go`. It is skipped unless it is given the program. The test
isolation clears the environment, so the program is a flag, not a variable.

```
go test ./internal/server -run 'TestRealOpencodeEndToEnd$' -count=1 -timeout 14m -v \
  -args -opencode=<absolute path of opencode.exe> -opencode-report=<file for the observations>
```

The report file gets the observations as JSON, and `<file>.rpc.log` gets the raw traffic. The traffic
is kept by `acptest`'s recording proxy (`--acptest-tee`), which sits between the client and the real
program and copies both directions to a log. The client does not keep raw traffic.

The run sends these prompts to opencode's hosted service, with no login, to its default free model
(`opencode/big-pickle`): "Reply with the single word pong", a request to run `echo e2e-ok`, a
request to run a `node` command that sleeps for a minute, "Reply with the single word pong2" and a
request to run `echo close-test`. Those are the test's own prompts. opencode may fetch other things on its own at startup, such as model lists, and the protocol log does not show those.

## What this record is

The numbers and claims below are one run's account. The report file and the raw traffic log are not
committed. The test itself asserts four things: the permission cards carry `rawInput.command`, the
cancelled command's process is gone three seconds after the cancel, the close answers the open request
cancelled, and no opencode or proxy process is left. Everything else here is recorded by the test and
not asserted.

## Isolation

- The workspace and its config are the usual temporary ones, and the test refuses to run if the
  config directory is not under the temporary directory. The agent's directories are checked against
  the real home directory, which the test isolation records before it moves the process's own.
- The agent's profile, local app data and XDG data, cache and state directories are one new
  directory at the root of the drive, outside the user profile. The pane works in another. Flockdeck
  passes an agent only its minimal environment and those names.
- The listing script (in the PR description) lists the real Flockdeck directories, the real opencode
  directories and the npm cache, with sizes and times, and the names in the profile root. The last
  run was bracketed by two listings. Nothing outside the running Flockdeck's own directories
  differed, and not even those differed in that window.

One thing did change earlier, and it was the install, not the test. `npm install opencode-ai` runs
the program once, with the real profile, and it created four empty directories there:
`.config/opencode`, `.local/share/opencode` (with `log` and `repos` inside), `.local/state/opencode` and
`.cache/opencode` (with `bin` inside). This is what the spike saw. They were empty and dated at the
install, and I removed them with `rmdir`, which only removes empty directories. A later install
should run with the profile variables set to a scratch directory.

## What the real agent did

opencode 1.19.3, from npm (`opencode-ai`, the Windows x64 program).

| Check | Result |
|---|---|
| Start with no login | Works. `initialize` lists an `opencode-login` method, but `session/new` and prompts succeed without it. |
| First prompt | The reply was `pong`. `usage_update` gave `used` 7,958 of `size` 200,000 and `cost` 0 USD. |
| Tool use, default configuration | The shell tool ran with no `session/request_permission` at all. The `tool_call` for it has `kind: execute` and `rawInput: {cwd}`; the next `tool_call_update` adds `rawInput.command`. |
| Tool use, with `{"permission":{"bash":"ask","edit":"ask"}}` in the working directory | A `session/request_permission` arrived with `kind: execute`, the command as the title, `locations: []`, and `rawInput: {"command": "echo e2e-ok"}`. Options were `once` (allow_once), `always` (allow_always) and `reject` (reject_once). |
| Answer from a relay window | Refused with the desk-only sentence, and the request stayed open. |
| Answer from the desk | Taken. The agent got `{"outcome":"selected","optionId":"once"}`, ran the command, and replied `e2e-ok`. |
| Cancel in the middle of a command | `structuredCancel` was accepted. The agent got `session/cancel` and ended the turn with `stopReason: cancelled`. The pane went idle with a "Turn cancelled." notice. The `node` process and its parent shell, found by a marker in the command line, were gone three seconds later. |
| Close with a request open | The pane was gone. The agent had been answered `{"outcome":"cancelled"}`. No opencode process and no proxy process was left. |
| Restart | A new `session/new` with a new session id. The workspace keeps the earlier entries and a "restarted" notice ahead of the new session. The new session answered `pong2`. |

## Findings

1. **`rawInput.command` is present on an `execute` permission request.** The card's `command` was
   filled from it for both requests. That is what T66 depended on. Two conditions apply. opencode
   must be configured to ask, since by default it asks for nothing. And a tool call that has not
   asked (the default) carries `command` only on its later updates, not on the first `tool_call`.
2. **By default opencode never asks for permission to run a shell command.** A pane run with an
   unconfigured opencode shows no permission cards, whatever the agent does. This is opencode's own
   default. The run above set `bash` and `edit` to `ask` in a project `opencode.json`. Since T124 the test
   writes no file: Flockdeck's own launch sets `OPENCODE_CONFIG_CONTENT` (the manifest's `launchEnv`), and the
   same run, with opencode 1.19.3, got the permission cards for both commands and passed. The real home
   directories were listed before and after and no opencode directory was made there.
3. **After a restart, `structuredSnapshot` and the watch push (then) held only the new session's entries. Fixed by #431.**
   The workspace's own list (`StructuredEntries`) holds the earlier sessions and the notice, and in
   this run `structuredSnapshot` returned two entries (the new prompt and its reply) against 12 in
   the workspace list. A drawer that followed the watch lost the earlier conversation at a restart.
   I did not change this in the run; #431 did, and the snapshot and the push now show the earlier
   sessions' entries first.
4. **The permission request's `locations` is empty for a shell command**, so the card has no file
   and the summary is `execute: <command>`.

## Not observed

The prompts above did not provoke these, so they are not observed, which is not the same as
unsupported: a `plan` update, an elicitation, and opencode's question tool. `usage_update` was
observed (four times, cost 0 USD). `agent_thought_chunk` (thinking) and `available_commands_update`
were also seen.

## What the fake agent covers

The fake agent's scenarios are exercised by the existing structured tests in `internal/server` and
`internal/structured`; this task adds nothing to them.
`TestTheRecordingProxyRelaysAndRecordsATurn` checks the proxy against the fake agent, with no network.
