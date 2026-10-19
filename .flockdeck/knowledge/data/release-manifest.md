---
type: File Format
title: Release manifest and site layout
description: Signed JSON files and directory layout the download site publishes for each release.
resource: internal/selfupdate/site.go
tags: [release, update, signing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/selfupdate/site.go]
---
## Site layout

| Path | Content |
|---|---|
| `/latest.json` | `{"version":"v1.2.3"}`. Unsigned, the only file that changes. |
| `/<version>/manifest.json` and `.sig` | Signed manifest. |
| `/<version>/<archive>` | Release archives. |
| `/<version>/checksums.txt` and `.sig` | SHA-256 lines, signed. |
| `/latest/<archive without version>` | For the site's download buttons. |
| `/recalled.json` and `.sig` | Signed list of withdrawn versions. |
| `/releases.json` and `.sig` | Signed list of recent releases. |

Everything under `/<version>/` is written once and cached for a year.

## manifest.json

`version`, `date` (UTC, to the second), `notes_url`, optional `notes`, and `files`: a list of `{name, url, sha256, size}`. `CheckManifest` requires a parseable version, plain file names (no slashes), a 64-hex SHA-256 per file, and entries for `checksums.txt` and `checksums.txt.sig`.

## recalled.json

`{"versions":[{"version","reason","upgrade"?}]}`.

## checksums.txt

One line per file: `<sha256>  <name>`; names are `flockdeck_<version>_<os>_<arch>.<zip|tar.gz|deb|rpm>`.

Written by [release](/tools/release.md), read by [selfupdate](/packages/selfupdate.md). See [release signature verification](/security/release-signature-verification.md).
