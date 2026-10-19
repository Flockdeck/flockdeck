---
type: HTTP Endpoint
title: POST /window
description: Returns a one-time window link so a second launch can open a window without putting the token on a command line.
resource: internal/server/attach.go
tags: [http, attach, auth]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go]
---
Handler: `Server.handleWindow`. `POST` only, URL token required.

Response: `200 text/plain`, `Cache-Control: no-store`, body is `http://<addr>/?w=<link>` from `WindowURL()`. The link works once, within `windowLinkLife` (1 minute), and [GET /](/api/index-page.md) exchanges it for the cookie. See [window link](/security/local-token-and-window-link.md).
