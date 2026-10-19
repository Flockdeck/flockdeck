---
type: CI Check
title: linux-packages workflow
description: Builds the deb and rpm packages and installs them in clean containers.
resource: .github/workflows/linux-packages.yml
tags: [ci, linux, packaging]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/linux-packages.yml]
---

Triggers: push or pull request touching `cmd/release/**`, `build/linux/**`, `build/appicon.png`, `LICENSE`, `THIRD-PARTY-NOTICES.md`, `go.mod`, `go.sum`, `internal/appwindow/**`, the smoke script or the workflow. Job `build` installs the GTK4/WebKitGTK headers and builds the packages; job `install` runs `.github/scripts/linux-package-smoke.sh` for each format and image. See [Linux packages](/build/linux-packages.md).
