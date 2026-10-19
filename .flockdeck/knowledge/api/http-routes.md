---
type: HTTP Endpoint
title: HTTP route table
description: Every route registered on the main server mux and which auth each requires.
resource: internal/server/server.go
tags: [http, routes, server]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go]
---
Registered in `server.New` on one `http.ServeMux`, wrapped by [host guard](/security/host-header-guard.md) for the loopback listener. The same mux is served through the relay tunnel by `RemoteHandler`, with the request marked as remote.

"Token" means the per-run token in `?t=` or the per-port cookie ([details](/security/local-token-and-window-link.md)); a request through the relay is authorised by arriving through the tunnel. "URL token" means the `?t=` token specifically ([details](/security/url-token-endpoints.md)): the cookie and the relay mark do not count.

| Method and path | Handler | Auth | Page |
|---|---|---|---|
| any `/` (only exact `/`) | `handleIndex` | token, or one-time link `?w=` | [index](/api/index-page.md) |
| `GET /assets/*` | `authFiles` | token | [assets](/api/assets.md) |
| `GET /help.json` | `handleHelp` | token | [help.json](/api/help-json.md) |
| `GET /ws/control` | `handleControl` | token | [control socket](/api/ws-control.md) |
| `GET /ws/pty` | `handlePTY` | token | [terminal socket](/api/ws-pty.md) |
| `GET /ws/artifacts` | `handleArtifacts` | relay only | [artifacts socket](/api/ws-artifacts.md) |
| `GET /health` | `handleHealth` | token and URL token | [health](/api/health.md) |
| `POST /open` | `handleOpen` | token and URL token | [open](/api/open.md) |
| `POST /quit` | `handleQuit` | token and URL token | [quit](/api/quit.md) |
| `POST /window` | `handleWindow` | token and URL token | [window](/api/window.md) |
| `POST /remote/reload` | `handleRemoteReload` | token and URL token | [remote reload](/api/remote-reload.md) |
| `POST /remote/artifacts/stop` | `handleArtifactsStop` | URL token, never remote | [artifacts stop](/api/remote-artifacts-stop.md) |
| `POST /helpers/start`, `/helpers/stop`, `/helpers/open` | `handleHelper*` | token and URL token | [helpers](/api/helpers-endpoints.md) |

The hook and spawn endpoints used by agents are not on this server; the hooks package has its own loopback listener.
