---
type: Developer Tool
title: docgen
description: Generates the documentation site from the in-app help plus hand-written self-hosting pages.
resource: cmd/docgen
tags: [docs, generator]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/docgen/main.go, ui/help/help.go, ui/help/render.go]
---
The "using Flockdeck" pages are rendered from `ui/help`, the same Markdown and key-table placeholders as the in-app F1 help, so the web copy cannot say more than the app does. Self-hosting pages are ordinary Markdown in the docs repository (`content/self-hosting`), which docgen only reads.

| Flag | Default | Meaning |
|---|---|---|
| `-out` | none, required | Directory to write (a checkout of the docs repository). |
| `-content` | `<out>/content/self-hosting` | Where the self-hosting Markdown lives. |
| `-url` | `https://docs.flockdeck.ai` | Address the site is served from. |
| `-repo` | the docs repo URL | Docs repository link. |

Writes `index.html`, per-page HTML, `docs.css`, favicons and fonts. Self-hosting pages follow a fixed order (`selfHostOrder`: overview, requirements, deploying-the-relay, configuration, storage-and-data, admin-and-invites, connecting-desktops, security-and-privacy, upgrading, troubleshooting). Renders with goldmark and `html/template`.

Run from `.github/scripts/regen-followup.sh` (`go run ./cmd/docgen -out <target>`). Uses assets also checked against [sitegen](/tools/sitegen.md) fonts.
