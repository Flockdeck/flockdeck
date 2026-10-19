---
type: Security Boundary
title: Host header allowlist
description: The loopback listener refuses any request whose Host is not 127.0.0.1, localhost or an operator-added name.
resource: internal/server/hostguard.go
tags: [security, http, dns-rebinding]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/hostguard.go, internal/server/remote.go]
---
`guardHosts` wraps the mux on the loopback listener only. A request whose `Host` is not allowed gets `403` with a plain-text explanation, before any handler or WebSocket upgrade.

Purpose: a page that reaches the same port through another name (a name that resolves to 127.0.0.1 after the page loaded is the usual way) sends that name in `Host`. The WebSocket library's own origin check accepts an `Origin` equal to `Host`, so it would not stop such a page.

Rules (`splitHost`, `validHostName`, `validPort`): printable ASCII only, max 300 characters, no user info, path or spaces, IPv6 only in brackets without zone, ports 1 to 65535, trailing dot and case ignored. Defaults: `127.0.0.1` and `localhost`, any port. Add more with [FLOCKDECK_ALLOWED_HOSTS](/config/flockdeck-allowed-hosts.md).

Refusals are logged to stderr at most once a minute with a count of skipped ones; the logged Host is cleaned to printable ASCII, 60 characters.

Relay requests are served by `RemoteHandler` and skip this guard; the relay's own device check admits them.
