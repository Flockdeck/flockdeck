---
type: Package
title: webui
description: Embeds the browser front end (HTML, JS, CSS, xterm.js, fonts, icons) into the binary.
resource: internal/webui/webui.go
tags: [webui, frontend, embed]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/webui/webui.go]
---
`internal/webui` is a `//go:embed assets` wrapper. It has two exported functions:

- `FS()` returns the `assets` directory as an `fs.FS` (via `fs.Sub`).
- `Icon()` returns `assets/icon-256.png` bytes, used as the native window icon by [appwindow](/packages/appwindow.md).

Everything else in the directory is tests (many `*_test.go`, some run the front end under `node`).

## Embedded assets

| Path | Content |
|---|---|
| `index.html` | The single page; loads xterm, addons, `e2e.js`, `app.js` as classic scripts, `app.css` |
| `app.js` | The whole application (about 16k lines, one `ACTIONS` table keyed by the key-table action ids) |
| `e2e.js` | WebCrypto port of `internal/e2e` for the terminal and artifacts sockets when reached through the relay |
| `app.css` | Styles |
| `vendor/` | `xterm.js`, `xterm.css`, `addon-fit.js`, `addon-webgl.js`, `addon-search.js` |
| `fonts/` | IBM Plex Sans and JetBrains Mono (`woff2`) with their OFL licence texts |
| `icon*.png/svg/ico` | Favicons, idle and waiting variants, 16 to 512 px |

There is no manifest file or service worker in the embedded set.

## Front-end and back-end contract

`app.js` derives `basePath` from `location.pathname` and opens `ws/control`, `ws/pty?id=<pane>&size=1[&from=&epoch=]` and `ws/artifacts` under it (so it works under a relay path prefix), and fetches `help.json` with same-origin credentials. Messages are described in [/ws/control](/api/ws-control.md), [server messages](/api/ws-server-messages.md), [/ws/pty](/api/ws-pty.md) and [/ws/artifacts](/api/ws-artifacts.md).

Tests enforce: every action id in the Go key table has an entry in `ACTIONS`, and no key binding string is written by hand into `app.js`, `index.html` or `app.css` (bindings come from the `hello` key table).

Served by [server](/packages/server.md) at [`/assets/`](/api/assets.md) and [`/`](/api/index-page.md).
