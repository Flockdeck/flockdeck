---
type: HTTP Endpoint
title: GET /assets/*
description: Serves the embedded front-end files to authorised requests, gzipped when reached through the relay.
resource: internal/server/server.go
tags: [http, assets]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go]
---
Handler: `Server.authFiles(webui.FS())` behind `http.StripPrefix("/assets/")`.

- Auth: token, cookie or relay mark; otherwise `403 forbidden`.
- Always sends `Cache-Control: no-store`. The page names assets without a version and the window keeps its own browser profile across runs, so a cached `app.js` could outlive the binary it came with.
- Through the relay only, when the client sends `Accept-Encoding: gzip` (a `q=0` refuses it) and the extension is `.js`, `.css`, `.html`, `.json` or `.svg`, the file is gzipped once at best compression, cached in memory for the life of the process, and sent with `Content-Encoding: gzip` and `Vary: Accept-Encoding`. Over loopback files are served as stored.
- Otherwise `http.FileServer` over the embedded FS.

Contents of the FS are listed in [webui](/packages/webui.md).
