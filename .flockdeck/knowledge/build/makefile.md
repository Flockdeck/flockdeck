---
type: Build Step
title: Makefile targets
description: Local build, test, cross-compile and package targets.
resource: Makefile
tags: [build, make]
timestamp: 2026-10-19T00:00:00Z
code_paths: [Makefile]
---

`CGO_ENABLED=0` is exported for every target, so any platform compiles from any machine with only the Go toolchain. The version string is `git describe --tags --always --dirty` (or `dev`) and is injected with `-X main.version`.

| Target | What it runs |
|---|---|
| `build` | `go build` to `flockdeck` (`flockdeck.exe` plus `flockdeck-chat.exe` on Windows) |
| `install` | `go install`; on Windows also builds the chat twin into the Go bin dir |
| `test` | `go test ./...` |
| `race` | `CGO_ENABLED=1 go test -race ./...` (needs a C toolchain) |
| `vet`, `fmt`, `check` | `go vet ./...`, `gofmt -l -w .`, `vet` then `test` |
| `dist` | one target per platform: windows, linux, darwin, each amd64 and arm64, into `dist/` |
| `package` | `go run ./cmd/release -version $(VERSION) -out dist`; see [release tool](/tools/release.md) |
| `installer` | Windows only: builds `flockdeck.exe`, fetches the WebView2 bootstrapper if absent, runs `makensis` on `build/windows/nsis/project.nsi` |

Windows binaries link with `-H=windowsgui`. The second Windows binary, `flockdeck-chat`, is the same program linked as a console program; see [windows chat twin](/gotchas/windows-chat-console-twin.md).
