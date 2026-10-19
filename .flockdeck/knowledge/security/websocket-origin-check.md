---
type: Security Boundary
title: WebSocket origin check
description: The control, terminal and artifacts sockets accept only pages loaded from the server's own address.
resource: internal/server/pty.go
tags: [security, websocket, origin]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/pty.go, internal/server/control.go, internal/server/artifacts_socket.go]
---
All three sockets call `websocket.Accept(w, r, opts)` with no `OriginPatterns`. The library's default then requires the `Origin` host to equal the request `Host` (a request with no `Origin`, such as a non-browser client, is allowed by the library, but still needs the token).

Why no patterns: cookies are shared across ports of one host and WebSockets are not subject to CORS. A dev server on another local port, or any page that can be served from 127.0.0.1 or localhost, would otherwise arrive with the cookie and could open the control socket (prompt every agent, open a shell) or type into a terminal. The comments record that the control socket once admitted any local-port page.

Related checks:

- `/ws/artifacts` additionally refuses a request with no `Origin` header (`403`) and any request not through the relay (`404`).
- `controlClient.page` is true only when `Origin` was sent; baton approvals are shown only to such connections ([baton approval](/security/baton-approval.md)).
- Index responses carry `frame-ancestors 'self'` (`'none'` through the relay) so another local page cannot frame the window.
- The [host header guard](/security/host-header-guard.md) covers the case where `Origin` equals `Host`.
