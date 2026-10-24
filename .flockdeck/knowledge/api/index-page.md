---
type: HTTP Endpoint
title: GET /
description: Serves the embedded index.html and turns a one-time link or token into a cookie.
resource: internal/server/server.go
tags: [http, auth, frontend]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go, internal/server/linkgone.go, ui/embed.go]
---
Handler: `Server.handleIndex` in `internal/server/server.go`. Only the exact path `/` is served; other paths under the catch-all return 404.

Flow:

1. If `?w=<link>` is present, `redeemLink` tries to use it (single use, valid for `windowLinkLife`, 1 minute, issued by [`WindowURL`](/security/local-token-and-window-link.md)).
2. If that did not succeed and the request is not authorised (query `t`, per-port cookie, or relay mark), a bad link goes to `serveLinkGone` (a styled page saying whether the link was used or expired, see `linkgone.go`); no link gives plain `403 forbidden`.
3. If the link was redeemed, or `?t=` matches the token, it sets the cookie (`HttpOnly`, `SameSite=Strict`, path `/`). A stale `t` is not promoted over a working cookie.
4. Reads `index.html` from [webui](/packages/webui.md) and returns it with `Cache-Control: no-store` and `Content-Security-Policy: frame-ancestors 'self'` (`'none'` when the request came through the relay).

Response: `text/html; charset=utf-8`; `500 missing front end` if the embedded file is absent.
