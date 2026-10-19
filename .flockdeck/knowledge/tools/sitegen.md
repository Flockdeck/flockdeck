---
type: Developer Tool
title: sitegen
description: Generates the public marketing site as plain files from this repository.
resource: cmd/sitegen
tags: [site, generator]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/sitegen]
---
Writes the landing page and trust, privacy, terms, refunds and licences pages, plus install scripts, icons and fonts, so site claims come from the code beside them. Markdown pages are rendered with goldmark; the licences page is built from the notices files. Output is plain files; the site is served from a repository of its own.

| Flag | Default | Meaning |
|---|---|---|
| `-out` | `site` | Output directory. |
| `-repo` | the GitHub repo URL | Source repository link. |
| `-module` | `github.com/jmwri/flockdeck` | Module path for `go install`; must match `go.mod`. |
| `-url` | `https://flockdeck.ai` | Address the site is served from. |
| `-release` | none | Release tag the install scripts install by default. |
| `-checksums` | none | Path to that release's `checksums.txt`, with `checksums.txt.sig` beside it. |

- The signature on `-checksums` is checked against the compiled-in release keys ([selfupdate](/packages/selfupdate.md)) before anything in it is trusted; archive SHA-256 values are then baked into `install.sh` (`.tar.gz`) and `install.ps1` (`.zip`), which verify what they download.
- Assets are embedded (templates, CSS, fonts, screenshots, licences, `sponsors.txt`). Files may get content-hashed names; stale ones are pruned. Fonts must match `cmd/docgen`'s copy (a test checks).
- `cmd/sitegen/shots` holds the screenshot tooling (not Go).
- Invoked from `.github/scripts/regen-followup.sh` with `GH_TOKEN` and `GITHUB_TOKEN` unset.

See [docgen](/tools/docgen.md) and [design tokens](/tools/design-tokens.md).
