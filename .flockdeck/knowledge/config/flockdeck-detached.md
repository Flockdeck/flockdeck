---
type: Environment Variable
title: FLOCKDECK_DETACHED
description: Internal marker the -detach re-launch sets on its child process (Unix builds).
resource: console_unix.go
tags: [config, internal]
timestamp: 2026-10-19T00:00:00Z
code_paths: [console_unix.go]
---
Not for users to set. On Unix, `detachFromTerminal` re-runs the program in a new session with `FLOCKDECK_DETACHED=1` added to the environment and the same arguments. A process that finds it non-empty unsets it and continues as the detached instance, ignoring SIGPIPE. The parent relays the child's startup output and exits. Set by [flockdeck](/cli/flockdeck.md) `-detach` ([FLOCKDECK_DETACH](/config/flockdeck-detach.md)).

The Windows console file does not read it.
