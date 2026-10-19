---
type: Security Boundary
title: Release signature verification
description: How the updater trusts a release: compiled-in Ed25519 keys, signed manifests and checksums, SHA-256 on every download.
resource: internal/selfupdate
tags: [security, update, signing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/selfupdate/site.go, internal/selfupdate/sign.go, internal/selfupdate/selfupdate.go, internal/selfupdate/releasekey.go]
---
Mechanism in [selfupdate](/packages/selfupdate.md); signing side in [release](/tools/release.md).

- **Keys are compiled in.** `releaseKey` (primary) and `releaseKeyStandby` are Ed25519 public keys in `releasekey.go`. `TrustedKeys()` returns both when valid. They are not fetched, because a key served beside the release would vouch for nothing. An unparseable value (a placeholder) is not trusted; with no trusted key, site reads fail with "no release key".
- **Signed files.** `.sig` holds standard base64 of an Ed25519 signature over the file's exact bytes. Signed: `manifest.json`, `checksums.txt`, `recalled.json`, `releases.json`. `VerifyAny` accepts a signature from any trusted key.
- **Unsigned pointer.** `latest.json` only names a version. The manifest it points to must pass the signature and name the same version. A forged or stale pointer can name only another signed release; pre-releases are refused and `Newer` never moves backwards.
- **GitHub fallback.** A release read from GitHub must carry `checksums.txt.sig`, also checked against the trusted keys, and the archive name must start with `flockdeck_<version>_` so an older signed archive cannot be republished under a newer tag.
- **Download.** The archive is hashed while written, the read is capped at the signed size plus one byte, and the hash must equal the one in the verified checksums. A mismatch deletes the file. When both a signed manifest and `checksums.txt` give a hash they must agree.
- **Recall.** `Recall` checks a signed `recalled.json` for versions known to be bad, covering copies that applied a release before it was pulled.
- **Apply.** Never done under a running session. Windows console twin goes in with the program or not at all.
- **Rotation.** The standby key's private half is offline; per `cmd/release`'s notes it signs one emergency release (private half supplied in the signing env var for that run only), then a rotated release carries the new keys. A copy that never updates keeps trusting what it holds.
- **Signing guard.** `release -sign` refuses a key not among `TrustedKeys()` unless `-test-key`, and re-reads its own outputs through `CheckManifest`/`CheckPointer`. See [FLOCKDECK_SIGNING_KEY](/config/flockdeck-signing-key.md).
- **Install scripts.** [sitegen](/tools/sitegen.md) verifies `checksums.txt.sig` against the same keys before baking archive hashes into `install.sh`/`install.ps1`.
- **Test hook.** `TrustKeysForTest` panics outside a test binary.
