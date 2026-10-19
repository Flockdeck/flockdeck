---
type: Security Boundary
title: Session token, cookie and one-time window link
description: How a local window proves it may drive the server: a per-run token, a per-port HttpOnly cookie and a single-use link.
resource: internal/server/server.go
tags: [security, auth, token]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go]
---
## Token

`server.New` listens on `127.0.0.1:0` and creates a token from 24 random bytes (`crypto/rand`, hex). It is compared with `subtle.ConstantTimeCompare`. A request is `authorised` when any of these hold:

1. it came through the relay tunnel (`fromRemote`, see [relay trust](/security/relay-trust-and-e2e.md));
2. `?t=` matches the token;
3. the cookie named `flockdeck_token_<port>` matches the token.

The token is also kept in the instance record in the state directory so a second launch can call the action endpoints ([URL token endpoints](/security/url-token-endpoints.md)). The code notes a process running as the same user can read it; the boundary is against other users and other web pages, not same-user malware.

## Cookie

Set by `GET /` with `HttpOnly`, `SameSite=Strict`, path `/`. Its name includes the listening port because cookies are scoped to a host, not a port, so two instances on 127.0.0.1 would overwrite each other's cookie ([gotcha](/gotchas/cookie-named-per-port.md)).

## One-time window link

`WindowURL()` returns `http://<addr>/?w=<random>` and stores the link for `windowLinkLife` (1 minute). `GET /` redeems it once (`redeemLink`), sets the cookie, and the page then carries no token in its URL. This exists because an address given to a browser stays on its command line for as long as the browser runs, where other users can read it (`ps`, `/proc/<pid>/cmdline`). The desktop window loads the link directly ([appwindow](/packages/appwindow.md)); a second launch asks `POST /window`. A used or expired link opens nothing; `serveLinkGone` explains which and keeps the fate of a spent link for a while (`linkFateRetention`).

Links are cleared on `Close`.
