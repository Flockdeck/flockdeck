---
type: Environment Variable
title: FLOCKDECK_DETACH
description: Environment form of the -detach flag: keep running without a window.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-detach`. True only for "1" or "true" (case-insensitive). A flag wins. See also [FLOCKDECK_DETACHED](/config/flockdeck-detached.md), the internal marker the detached child carries.
