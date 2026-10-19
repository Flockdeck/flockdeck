---
type: Environment Variable
title: FLOCKDECK_REMOTE_INVITE
description: Default invitation code for remote enable, for relays that require one.
resource: cli_remote.go
tags: [config, remote, secret]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_remote.go]
---
Seeds the default of `-invite` on [remote](/cli/remote.md) `enable`. The flag wins. `remote move` does not read this variable.
