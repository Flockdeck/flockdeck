---
type: Security Boundary
title: Helper install and launch verification
description: Signature, hash, version, archive and pre-start checks that keep a downloaded helper from doing more than asked.
resource: internal/helpers/install.go
tags: [security, helpers, supply-chain]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/helpers/install.go, internal/helpers/catalogue.go, internal/helpers/verify_install.go, internal/helpers/env.go, internal/helpers/extract.go, internal/helpers/fetch.go, internal/helpers/version.go, internal/helpers/layout.go]
---
Nothing from a downloaded archive runs until the user starts the helper. An install must pass, in order: the signature on the release manifest and `checksums.txt` (compiled-in keys; a wrong signature is never overridable, only a missing one can be), the version rules, the archive's SHA-256, and the archive checks in `extract.go`.

## Checks

- Network: `SourceAllowURL` allows only https, the exact host of the entry's `Source`, port 443, no credentials. Redirects are not followed.
- Version: `Entry.MinVersion` is a floor even for signed releases; a per-helper trust record keeps a high-water version so an old signed release cannot be served again, and an unreadable or damaged record refuses the install (`TrustReadError`). `Plan` is sealed with a per-installer key.
- Extraction: one top folder `<id>_v<version>_<os>_<arch>`, required binary, caps on file count (default 2000), total unpacked bytes (default 256 MiB), per-file bytes and compression ratio (`ArchiveError`).
- Ids and versions used in paths pass `validID` (`^[a-z][a-z0-9-]{0,31}$`) and `validVersion`.
- Before every start, `VerifyInstall` checks folders are real (no symlink, junction or reparse point), the binary is a plain file and its SHA-256 matches `install.json`'s `binarySha256`. Failure says to reinstall. The code states it does not defend against a program already running as the user that can rewrite both files.
- Launch: environment is an allowlist (`PATH`, home/temp/system dirs, locale names, proxy variables, `SSL_CERT_FILE`) matched by ASCII upper-case, plus the entry's own variables. The helper listens on 127.0.0.1 on a port Flockdeck picks; its first stdout line must match the entry's banner with that port, and the OS port owner is checked where supported.
- Proxy variables are passed as they are and can carry credentials; the "allows" text shows this to the user.

See [helpers](/packages/helpers.md).
