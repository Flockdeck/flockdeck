---
type: Gotcha
title: Embedded assets are served no-store
description: index.html, assets and help.json send no-store so an old app.js cannot talk to an upgraded server.
resource: internal/server/server.go
tags: [http, cache, frontend]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/server.go]
---
The page references `assets/app.js` and the rest without a version in the path, the desktop window keeps its own browser profile between runs, and embedded files have no modification time to revalidate against. `index.html`, every file under `/assets/` and `/help.json` therefore send `Cache-Control: no-store`.

Cost is borne through the relay, where a phone on a metered link would refetch about a megabyte of script on each load. The server gzips `.js`, `.css`, `.html`, `.json`, `.svg` and the help JSON for relay requests that accept it, compressing each file once per process. Over loopback nothing is compressed.

The `webui` tests also enforce that bindings are never hard-coded into the front end (they come from the `hello` key table), so the UI cannot drift from the server's key table.
