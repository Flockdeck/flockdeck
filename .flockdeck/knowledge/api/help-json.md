---
type: HTTP Endpoint
title: GET /help.json
description: Returns the compiled-in help pages as JSON.
resource: internal/server/help.go
tags: [http, help]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/help.go]
---
Handler: `Server.handleHelp` (`internal/server/help.go`).

- Auth: token, cookie or relay mark; else `403`. Any method is accepted.
- Body: `{"pages": [...]}` from `help.Pages()`, encoded once per process (`sync.OnceValues`). `500` "the help pages could not be rendered" on failure.
- Headers: `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store`. Through the relay with `Accept-Encoding: gzip` it is sent gzipped (key `/help.json` in the gzip cache).

The front end fetches it once per page load with `credentials: "same-origin"`.
