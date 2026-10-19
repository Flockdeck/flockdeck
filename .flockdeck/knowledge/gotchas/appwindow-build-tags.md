---
type: Gotcha
title: appwindow build tags: cgo or Windows
description: The native window is compiled only with cgo or on Windows; other builds get a stub whose Open always fails.
resource: internal/appwindow/appwindow_nocgo.go
tags: [build, appwindow, windows, docker]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/appwindow/appwindow_nocgo.go]
---
`appwindow.go` is built with `cgo || windows`; `appwindow_nocgo.go` with `!cgo && !windows`. Wails' Linux (WebKitGTK) and macOS (Cocoa) backends need cgo; its Windows backend uses raw syscalls to WebView2 and needs none.

An earlier split used only `cgo` for the real file, so every Windows release (built with `CGO_ENABLED=0`) got the stub and fell back to a browser tab with a message about a "headless server". The tag now includes `windows`. The static Docker image uses the stub, which is safe because `-no-window` and `-detach` never call `Open`.

Also: Wails' application object is a process singleton, so `Open` returns an error on a second call, and `Open` and `Run` must be called from the main goroutine (the OS thread is locked on first use). `defaultbrowser.go` and `reveal*.go` have no cgo requirement and exist in both builds.
