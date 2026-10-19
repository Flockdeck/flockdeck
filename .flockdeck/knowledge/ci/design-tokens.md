---
type: CI Check
title: design-tokens workflow
description: Contrast and colour-vision checks on the design token files.
resource: .github/workflows/design-tokens.yml
tags: [ci, design]
timestamp: 2026-10-19T00:00:00Z
code_paths: [.github/workflows/design-tokens.yml]
---

Triggers: push or pull request touching `design/**` or the workflow file. Job `contrast` has two steps: the contrast, colour-vision and report check (`design/check-contrast.mjs`) and the design package tests. See [design tokens](/tools/design-tokens.md).
