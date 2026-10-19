---
type: Environment Variable
title: FLOCKDECK_REMOTE_NAME
description: Default machine name for remote enable, for first-boot scripts.
resource: cli_remote.go
tags: [config, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_remote.go]
---
Seeds the default of `-name` on [remote](/cli/remote.md) `enable`. Without it or the flag, the machine's host name is used. The flag wins.
