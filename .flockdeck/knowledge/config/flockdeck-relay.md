---
type: Environment Variable
title: FLOCKDECK_RELAY
description: Overrides the default relay address used when enabling remote access.
resource: internal/remote/config.go
tags: [config, env, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/remote/config.go]
---
Constant `remote.RelayEnv`. Read in `RelayURL(named)`: the relay named by `-relay` or in the window wins, then this variable, then `DefaultRelay` (`https://remote.flockdeck.ai`).

The value goes through `CheckRelay` (see [relay address validation](/security/relay-address-validation.md)); an error from an env value says it came from `FLOCKDECK_RELAY`.

It matters only at enrolment. After that the address in [remote.json](/data/remote-config-file.md) is used. The old default `https://relay.flockdeck.ai` is treated as the same relay (`SameRelay`).
