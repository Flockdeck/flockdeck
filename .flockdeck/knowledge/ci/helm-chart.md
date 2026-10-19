---
type: CI Check
title: helm-chart workflow
description: Lints and renders the Helm chart and validates the manifests.
resource: .github/workflows/helm-chart.yml
tags: [ci, helm]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/helm-chart.yml]
---

Triggers: push or pull request touching `deploy/helm/flockdeck/**`. Job `lint-and-template` runs `helm lint --strict`, `helm template` with defaults, `helm template` with persistence and remote enabled, and `kubeconform` on the output. Chart: [Helm chart](/build/helm-chart.md).
