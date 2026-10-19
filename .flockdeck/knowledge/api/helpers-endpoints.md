---
type: HTTP Endpoint
title: POST /helpers/start, /helpers/stop, /helpers/open
description: Lets the CLI start, stop or open a helper app run by this instance.
resource: internal/server/helpers.go
tags: [http, helpers, cli]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/helpers.go]
---
Handlers: `handleHelperStart`, `handleHelperStop`, `handleHelperOpen`, all through `helperAction`: token, `POST`, URL token, then `503` if no supervisor was set (`SetHelpers`), `400` if `id` is missing or `helpers.Lookup(id)` does not know it.

Query: `id` (helper id), `t`.

| Path | Action | Response |
|---|---|---|
| `/helpers/start` | `409` if the helper is being installed or `Start` fails; else waits up to 90 s (`helperStartWait`) for running | `helpers.Status` JSON |
| `/helpers/stop` | `Stop(id)`, `409` on error | `helpers.Status` JSON |
| `/helpers/open` | needs state running with a URL (`409` otherwise); opens it with `appwindow.OpenDefault` (`502` on failure) | `helpers.Status` JSON |

Client: `server.HelperRequest(baseURL, token, action, id)`; redirects are not followed, and the token is stripped from any error text (`redactHelperToken`). The window uses control commands (`helperStart`, `helperStop`, `helperOpen`) instead, see [settings commands](/api/ws-commands-settings.md).
