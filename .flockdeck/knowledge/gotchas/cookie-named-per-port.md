---
type: Gotcha
title: The token cookie is named per port
description: Cookies ignore ports, so each instance names its cookie flockdeck_token_<port>.
resource: internal/server/server.go
tags: [auth, cookie, solo]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go]
---
Cookies belong to a host and take no notice of the port. Two instances on 127.0.0.1 (a `-solo` run beside the normal one, whose windows share one browser profile in the state directory) used to write the same cookie; the second window to load replaced the first one's token. The first window then worked until it opened a new socket and was refused by its own instance.

`cookieName()` returns `flockdeck_token_<listener port>`. A request is authorised by the cookie only if it carries this instance's name and token.

Consequence for security design: the cookie is sent to every local port on the same host, so it is not accepted as proof for [action endpoints](/security/url-token-endpoints.md), and sockets check origin ([origin check](/security/websocket-origin-check.md)).
