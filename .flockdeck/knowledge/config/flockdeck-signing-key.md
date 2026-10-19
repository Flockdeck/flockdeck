---
type: Environment Variable
title: FLOCKDECK_SIGNING_KEY
description: Release signing key read by `release -sign`.
resource: cmd/release/sign.go
tags: [release, signing, secrets]
timestamp: 2026-10-19T00:00:00Z
code_paths: [cmd/release/sign.go]
---
- **Default:** unset; `-sign` fails with a message if it is empty.
- **Read by:** `cmd/release` (`signingKeyEnv`), passed to `selfupdate.ParseSigningKey`.
- **Format:** an Ed25519 private key as PKCS#8 PEM, or one line of base64 holding the 32-byte seed (what `release -keygen` writes). Errors never quote the value.
- **Set by:** the release workflow, from a repository secret, in the signing step.
- **Standby key:** the offline standby private half is supplied here for a single emergency run only, never as the repository secret.
- **Check:** the key's public half must equal a key from `selfupdate.TrustedKeys()` unless `-test-key` is given.

See [release](/tools/release.md) and [release signature verification](/security/release-signature-verification.md).
