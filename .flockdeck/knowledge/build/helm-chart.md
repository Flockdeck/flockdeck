---
type: Build Step
title: Helm chart
description: Chart that runs the container image plus a port-proxy sidecar.
resource: deploy/helm/flockdeck/values.yaml
tags: [helm, kubernetes, deploy]
timestamp: 2026-10-19T00:00:00Z
code_paths: [deploy/helm/flockdeck/values.yaml]
---

Templates: `deployment`, `service`, `pvc`, `secret`, `serviceaccount`, `NOTES.txt`, `_helpers.tpl`.

Pod:

- Container `flockdeck`: image `ghcr.io/flockdeck/flockdeck`, tag defaults to the chart's `appVersion`. Runs as non-root uid/gid 65532 with a read-only root filesystem, a `tmp` volume, a `state` volume (persistence on by default, 1Gi) and an optional `workspace` volume (off by default).
- Container `portproxy`: same image, command `/usr/local/bin/flockdeck-portproxy`, listens on `sidecar.port` (8080), mounts `state` read-only. See the [portproxy tool](/tools/portproxy.md).
- Probes run `/usr/local/bin/flockdeck-healthcheck` ([container image](/build/docker-image.md)).

Values map to environment variables:

| Value | Variable |
|---|---|
| `dir` | `FLOCKDECK_DIR` |
| `agent` | `FLOCKDECK_START_AGENT` |
| `fresh`, `shellFirst`, `solo` | `FLOCKDECK_FRESH`, `FLOCKDECK_SHELL_FIRST`, `FLOCKDECK_SOLO` |
| `remote.url`, `.name`, `.join`, `.invite` | `FLOCKDECK_RELAY`, `FLOCKDECK_REMOTE_NAME`, `FLOCKDECK_REMOTE_JOIN`, `FLOCKDECK_REMOTE_INVITE` |
| `apiKey` | `FLOCKDECK_API_KEY` |
| `allowedHosts` | `FLOCKDECK_ALLOWED_HOSTS` |
| fixed in the template | `FLOCKDECK_UPDATE` |

The join code, invite code and API key come from a chart-made Secret, or from `remote.existingSecret` / `apiKey.existingSecret` so they stay out of Helm's release record. Checked by the [helm-chart workflow](/ci/helm-chart.md).
