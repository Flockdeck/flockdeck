---
type: Package
title: webui
description: Embeds the browser front end (HTML, JS, CSS, xterm.js, fonts, icons) into the binary.
resource: internal/webui/webui.go
tags: [webui, frontend, embed]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/webui/webui.go]
---
`internal/webui` is a `//go:embed ui/assets` wrapper. It has two exported functions:

- `FS()` returns the `ui/assets` directory as an `fs.FS` (via `fs.Sub`).
- `Icon()` returns `icon-256.png` bytes, used as the native window icon by [appwindow](/packages/appwindow.md).

It also registers the JavaScript type for `.mjs`, since on Windows Go reads it from the registry, which has none. Everything else in the directory is tests; the shell's own tests are the node tests in `ui/test`, run by `next/next_test.go`. The directory is called `next` for history: it is the only front end (the old one was deleted in T162).

## Embedded assets

| Path | Content |
|---|---|
| `index.html` | The single page: a module script, `assets/main.js`, and the stylesheets |
| `main.js`, `shell/`, `graph/`, `drawer/`, `lib/` | The team-graph shell: Preact and htm ES modules, no bundler |
| `e2e.js` | WebCrypto port of `internal/e2e` for the terminal and artifacts sockets when reached through the relay |
| `app.css` | Tokens and layout |
| `vendor/` | Preact, hooks and htm (`.mjs`) and the terminal library: `xterm.js`, `xterm.css`, `addon-fit.js`, `addon-webgl.js`, `addon-search.js`. The terminal library loads on first use (`lib/load-script.js`) |
| `fonts/` | IBM Plex Sans and JetBrains Mono (`woff2`) with their OFL licence texts |
| `icon*.png/svg/ico` | Favicons, idle and waiting variants, 16 to 512 px |

There is no manifest file or service worker in the embedded set.

## Front-end and back-end contract

The shell derives its base path from `location.pathname` and opens `ws/control`, `ws/pty?id=<pane>&size=1[&from=&epoch=]` and `ws/artifacts` under it (so it works under a relay path prefix), and fetches `help.json` with same-origin credentials. Messages are described in [/ws/control](/api/ws-control.md), [server messages](/api/ws-server-messages.md), [/ws/pty](/api/ws-pty.md) and [/ws/artifacts](/api/ws-artifacts.md).

Tests enforce: no key binding string is written by hand into the shell (bindings come from the `hello` key table), every asset obeys the content policy (`TestNextAssetsKeepToTheContentPolicy`), and `cmd/parity` holds the list of what the old interface did that this one does not yet.

Served by [server](/packages/server.md) at [`/assets/`](/api/assets.md) and [`/`](/api/index-page.md).
