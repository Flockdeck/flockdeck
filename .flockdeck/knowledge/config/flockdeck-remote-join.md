---
type: Environment Variable
title: FLOCKDECK_REMOTE_JOIN
description: Default join code for remote enable, to join another machine's account.
resource: cli_remote.go
tags: [config, remote, secret]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cli_remote.go]
---
Seeds the default of `-join` on [remote](/cli/remote.md) `enable`. The code comes from [remote pair](/cli/remote-pair.md) `-desktop` on a machine already on the account, and works once. The flag wins. `remote move` does not read this variable.
