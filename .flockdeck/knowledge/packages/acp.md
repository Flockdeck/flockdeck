---
type: Package
title: acp
description: Client for the Agent Client Protocol v1 over stdio, with a fake agent for tests.
resource: internal/acp/client.go
tags: [acp, agents, package]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/acp/client.go, internal/acp/calls.go, internal/acp/types.go, internal/acp/errors.go, internal/acp/acptest/agent.go, internal/acp/acptest/launch.go]
---
- `Start(Config)` launches an agent (absolute path) under [proctree](proctree.md); `Initialize`, `Authenticate`, `NewSession` (absolute cwd), `Prompt`, `Cancel`, `Close`. No `session/load`, `session/resume`, fs or terminal methods.
- Responses and notifications run on the read loop in wire order, so `Handler.Update` must not block. Only agent-to-client requests run on their own goroutine (`Handler.Permission`, with a context).
- Unknown update kinds, content block types and fields are delivered with their raw JSON. Request ids are echoed raw. Lines are read with `ReadBytes`; stderr is drained.
- The agent is untrusted: writes go through a bounded queue and one writer goroutine, so `Close`, `Cancel` and calls return even if the agent stops reading; stdout lines are capped (`MaxLineBytes`, 64 MiB), stderr lines at 64 KiB; agent requests in flight are capped (`MaxAgentRequests`, 32); a nil `Config.Env` is `MinimalEnv`, not the parent environment; initialize and session/new time out (`SetupTimeout`); handler panics are recovered; unusable input is counted by `ProtocolErrors`.
- `-32000` becomes `*AuthRequiredError` carrying the auth methods from initialize (a needs-you state).
- `Cancel` and `Close` answer open permission requests cancelled; a later answer is dropped.
- `acptest` is the fake agent: the test binary run again, chosen by `RunIfAgent` in TestMain. The contract tests in `contract_test.go` cover a plain turn, tool calls, permission allow, reject and cancelled, cancel mid-command, plan, a late permission answer, auth failure, unknown kinds, a 5 MiB tool result and string ids. The opencode test needs the `acp_opencode` tag and an environment variable.
- `Config.Tap(dir, raw)` is given every line read (`TapIn`, before parsing, so a line that is not JSON is seen) and written (`TapOut`, by the writer, just before the write). It runs on the read loop and the writer goroutine and must not block or change `raw`; a panic is counted in `ProtocolErrors`. A slow tap delays reading but does not reorder updates. The wiring to `record.RPCRecorder.Tap` is not here: this package does not import `internal/record`.
