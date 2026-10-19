---
type: Developer Tool
title: portproxy
description: Fixed-port TCP forwarder sidecar for the Kubernetes deployment.
resource: cmd/portproxy
tags: [kubernetes, sidecar, network]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/portproxy/main.go, internal/store/store.go]
---
The server binds 127.0.0.1 on a random port each start, so a Service has no stable target. `portproxy` listens on a fixed address and forwards every byte of every connection to whatever port the instance record names.

| Flag | Default | Meaning |
|---|---|---|
| `-listen` | `:8080` | Address to accept connections on. |

- On each connection it reads the [instance file](/data/instance-file.md) (`store.LoadInstance`), takes host:port from the recorded URL, and dials it with a timeout. Re-reading per connection picks up a restart on a new port.
- Plain TCP relay, not HTTP-aware: WebSocket upgrades and the token in the query string pass through unchanged. It adds no authorization; callers still need the token.
- Stops on SIGINT or SIGTERM.
- Built as a second binary (`flockdeck-portproxy`) in the same container image by the Dockerfile and run as the second container by the Helm chart. See [docker image](/build/docker-image.md) and [helm chart](/build/helm-chart.md).
