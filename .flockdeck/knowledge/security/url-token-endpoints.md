---
type: Security Boundary
title: Action endpoints need the token in the URL
description: /health, /open, /quit, /window, /remote/reload and /helpers/* require ?t= and POST, so cookies and relay requests cannot reach them.
resource: internal/server/attach.go
tags: [security, http, csrf]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/attach.go]
---
`requireURLToken` accepts only a `t` query parameter that matches the token. `requirePost` rejects other methods with `405` and `Allow: POST`. Both are applied after `authorised`.

Reason (from the code comments): these endpoints are for another launch of the binary. A cookie is no proof of who is asking, since cookies ignore the port, so anything served from 127.0.0.1 or localhost had the cookie attached to requests to this server. A cross-origin POST needs no permission to be sent, even if the reply cannot be read, and `/quit` stops every agent. A page from elsewhere does not have the URL token, cannot read it from replies, and cannot read the `HttpOnly` cookie.

`GET /health` and `POST /remote/artifacts/stop` use the same URL-token check; `/health` is GET because it only reports. Through the relay none of these succeed, because the relay holders never have the token. `/remote/artifacts/stop` also refuses relay requests explicitly.

Clients (`Probe`, `RequestQuit`, `HelperRequest`, `RequestRemoteReload`) put the token in the query string; `HelperRequest` removes it from error text and does not follow redirects.
