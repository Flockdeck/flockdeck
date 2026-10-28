---
type: Package
title: server
description: The loopback HTTP and WebSocket server that serves the embedded front end and drives the workspace for every window, local or remote.
resource: internal/server/server.go
tags: [server, http, websocket, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go, internal/server/remote.go, internal/server/attach.go, internal/server/hostguard.go, internal/server/control.go, internal/server/helpers.go, internal/server/pty.go, internal/server/artifacts_socket.go, internal/server/approvals_socket.go, main.go]
---
`internal/server` exposes the workspace to the browser front end. It listens on `127.0.0.1:0` (a free port), generates a 24-byte random hex token per run, and serves the page, assets, three WebSockets and a few action endpoints.

## Main pieces

| Piece | File | Role |
|---|---|---|
| `New(ws)` | `server.go` | Starts the listener, registers routes, starts background loops (push, git, usage, layout save, waits, conversation poll) |
| `Server.WindowURL()` / `URL()` | `server.go` | One-time window link vs. token URL, see [window link and token](/security/local-token-and-window-link.md) |
| `handleControl` | `control.go` | The control socket, see [/ws/control](/api/ws-control.md) |
| `handlePTY` | `pty.go` | Terminal socket, see [/ws/pty](/api/ws-pty.md) |
| `handleArtifacts` | `artifacts_socket.go` | Encrypted artifacts socket, see [/ws/artifacts](/api/ws-artifacts.md) |
| `handleApprovals` | `approvals_socket.go` | Encrypted phone approval socket, see [/ws/approvals](/api/ws-approvals.md) |
| `RemoteHandler`, `ServeRemote` | `remote.go` | Serves the same mux to connections arriving through the relay tunnel |
| `guardHosts` | `hostguard.go` | Host header allowlist, see [host guard](/security/host-header-guard.md) |
| `Probe`, `RequestQuit`, `HelperRequest`, `RequestRemoteReload` | `attach.go`, `helpers.go`, `remote.go` | Client side used by a second launch of the binary |

## Concurrency model

The workspace is not safe for concurrent use. Every access goes through one goroutine: `Server.do(fn)` queues work, `ask` queues work and returns the result. Panics in a command are recovered by `guard`/`guardFor`, written to `error.log` in the state dir, and shown to the window. See [workspace goroutine](/gotchas/workspace-goroutine-only.md).

## Routes

See [HTTP routes](/api/http-routes.md) and the individual endpoint files under `/api/`.

## Dependents and disk use

Imported by the root `main.go` and the `cli_*.go` files (attach, quit, helpers, remote, updates). Imports `workspace`, `store`, `remote`, `webui`, `helpers`, `spend`, `e2e`, `artifacts`, `record`, `recordview`, `appwindow` (to open URLs and reveal files).

Writes under the state dir: `error.log` (recovered panics), `remote-artifacts.log` ([audit log](/data/artifacts-audit-log.md)), `remote-approvals.log` ([audit log](/data/approvals-audit-log.md)), prefs via `store`. Reads `FLOCKDECK_ALLOWED_HOSTS` ([config](/config/flockdeck-allowed-hosts.md)). Network: outbound to GitHub through the `gh` CLI, git, and (for remote) the relay through [remote](/packages/remote.md).
- `permissionAnswer {id, cardSeq, requestId, optionId}` (`permission_answer.go`) answers a structured pane's pending permission card. `cardSeq` is a number the bridge gives each card (`bridge.Card.Seq`); the agent's request id can be reused, so the answer must match both, and an answer for an earlier card is stale for a later one. It is `accessFull` and refused to every window reached through the relay, full devices included (the comment-marked line `permission-answer-relay` is the one to lift when a sealed team socket exists). Option ids are compared as strings with the ids on the card; no kind is looked at. It refuses an unknown id, a stale card, a pane that is not structured and a pane with nothing pending. The pane is a `StructuredPane` looked up through `SetStructuredPanes`, none until the workspace sets it (P5); its `AnswerPermission` must be atomic and must not block, and `internal/server/structuredtest` is the conformance test the adapter runs. The log line names pane, card, request and option kind, never the tool or command.
