---
type: Build Step
title: Container image
description: Multi-stage Dockerfile producing a headless Flockdeck on Alpine.
resource: Dockerfile
tags: [docker, deploy]
timestamp: 2026-10-19T00:00:00Z
code_paths: [Dockerfile]
---

Stages:

1. `golang:1.27` builds `/out/flockdeck` and `/out/flockdeck-portproxy` (from `cmd/portproxy`) with `CGO_ENABLED=0 -trimpath -ldflags "-s -w"`. The `VERSION` build arg goes to `-X main.version`.
2. `alpine:3.20` adds `ca-certificates git bash tini jq`, creates user and group `flockdeck` (uid/gid 65532) and `/workspace`.

Runtime settings:

| Item | Value |
|---|---|
| Entrypoint | `/sbin/tini -- /usr/local/bin/flockdeck` |
| Default command | `-no-window` |
| Volumes | `/home/flockdeck`, `/workspace` |
| Port | 8080 (exposed) |
| `FLOCKDECK_UPDATE` | `off` |
| Healthcheck | `docker/healthcheck.sh` every 30s |

The healthcheck reads `instance.json` under `$HOME/.config/flockdeck/`, takes its `url` and `token`, and requests `<url>/health?t=<token>` with `wget`. It fails if the record or either field is missing.

Alpine is used instead of a minimal image because panes spawn shells, git and agent CLIs. Licence files are copied to `/licenses/`. Built and signed by the [docker-image workflow](/ci/docker-image.md); deployed by the [Helm chart](/build/helm-chart.md).
