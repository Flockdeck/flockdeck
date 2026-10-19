---
type: Environment Variable
title: FLOCKDECK_DIR
description: Default workspace directory for the root command; the environment form of -C.
resource: main.go
tags: [config]
timestamp: 2026-10-19T00:00:00Z
code_paths: [main.go]
---
Seeds the default of [flockdeck](/cli/flockdeck.md) `-C`. An empty value is treated as unset (the default is then "."). A `-C` on the command line wins.

Setting it counts as naming a directory even without `-C` (`dirWasGiven`), so a service that sets only this variable does not fall back to the working-directory logic.
