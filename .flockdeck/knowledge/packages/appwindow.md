---
type: Package
title: appwindow
description: Opens Flockdeck's UI in a native Wails webview window and offers browser and file-manager helpers.
resource: internal/appwindow/appwindow.go
tags: [appwindow, wails, desktop]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/appwindow/appwindow.go]
---
## Native window

`appwindow.Open(Config, target)` creates a Wails v3 application with one webview window (1440x900, centred, background colour `#0f1418`) pointed at `target`, which is a `server.WindowURL()` one-time link. `Run()` blocks on the native event loop; `Close()` is safe to call repeatedly. `Open` returns an error if called twice in a process. The window is the same HTTP client as any browser, not a separate asset path.

Build tags:

| File | Build | Behaviour |
|---|---|---|
| `appwindow.go` | `cgo || windows` | Real Wails window |
| `appwindow_nocgo.go` | `!cgo && !windows` | `Open` always returns an error ("built for a headless server"); used by the static Docker build |

See [build tags gotcha](/gotchas/appwindow-build-tags.md).

## Helpers (all builds)

- `OpenDefault(url)` (`defaultbrowser.go`): runs `rundll32 url.dll,FileProtocolHandler` (Windows), `/usr/bin/open` (macOS) or `xdg-open`, with no shell, and reaps the process in a goroutine. Used as the fallback when no webview works and by the helper-app open endpoint.
- `RevealFile(path)` (`reveal.go`): shows a file in the file manager. Path must be absolute for the target OS, not start with `-`, contain no NUL/CR/LF (and no `"` on Windows). Windows runs `explorer.exe /select,"path"` (exit code ignored); macOS `open -R`; Linux tries `dbus-send` FileManager1.ShowItems, then `xdg-open` on the folder. 5 second timeout per step, never through a shell.

Dependents: root `main.go`, `cli_recordings_export.go`, `internal/server` (`helpers.go`, `recording.go`). Uses `webui.Icon()` through the caller.
