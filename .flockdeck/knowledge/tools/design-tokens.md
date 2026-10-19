---
type: Developer Tool
title: design tokens
description: design/ folder holding tokens.css, the Go test helper for it and the Node contrast checker.
resource: design
tags: [design, ci, accessibility]
timestamp: 2026-10-19T00:00:00Z
code_paths: [design/tokens.css, design/tokens.go, design/check-contrast.mjs]
---
`design/tokens.css` is the single source of truth for colour, type, space, shape and motion across the desktop app, site, docs and remote client. Dark is the default `:root`; light is `[data-theme="light"]` (or the system, under `[data-theme="system"]`). Custom properties use the `--fd-` prefix. Fonts: IBM Plex Sans and JetBrains Mono (variable woff2 in `design/fonts`, OFL licence texts beside them). Each surface ships its own copy of the fonts.

## tokens.go (package `design`)

Embeds `tokens.css` (`TokensCSS`); nothing outside tests imports it. Helpers for surface tests: `Tokens(theme)` (canonical values for `Dark` or `Light`), `RootBlock(css, selector)`, `Declared(block)`, `AssertSurface(t, name, css, selector, theme, mapping)` mapping a surface's variable names to token names.

## check-contrast.mjs

```
node design/check-contrast.mjs                 print report, exit 1 on any failure
node design/check-contrast.mjs --md            markdown (design/contrast-report.md)
node design/check-contrast.mjs --check-report  CI form: failures only, also fails if contrast-report.md is stale
```

Reads `#RRGGBB` values from `tokens.css`. Contrast is WCAG 2.1 relative luminance. Colour-vision checks use Machado et al. (2009) simulation at severity 1.0, with OKLab distance (x100). Needs only Node.

CI: `.github/workflows/design-tokens.yml` runs `--check-report`.
