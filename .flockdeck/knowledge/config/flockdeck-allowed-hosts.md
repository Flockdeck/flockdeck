---
type: Environment Variable
title: FLOCKDECK_ALLOWED_HOSTS
description: Extra Host header values the loopback server answers to, for reaching the UI through another name.
resource: internal/server/hostguard.go
tags: [config, env, server, security]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/server/hostguard.go]
---
Read once in `server.New` via `os.Getenv(AllowedHostsEnv)`.

- Default allowed hosts: `127.0.0.1` and `localhost`, any port. `[::1]` is not allowed because the listener is IPv4 only.
- Value: comma-separated list. `host` allows any port (or none); `host:port` allows that port only. IPv6 in brackets. Case and a trailing dot are ignored. No wildcards, schemes, paths or user info.
- An invalid entry makes `New` return an error that names the entry, so a typo ends the run with a message instead of a server that refuses everything.
- Use when the UI is reached through a Kubernetes Service, an Ingress or a hostname.
- Applies to the loopback listener only; relay traffic does not pass through the guard.

See [host header guard](/security/host-header-guard.md).
