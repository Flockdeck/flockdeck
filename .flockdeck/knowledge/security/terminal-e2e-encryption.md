---
type: Security Boundary
title: Terminal end-to-end encryption
description: Per-socket authenticated key exchange and AEAD framing so the relay cannot read remote terminal traffic.
resource: internal/e2e
tags: [security, crypto, remote]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/e2e]
---
Implemented by [e2e](/packages/e2e.md).

- **Keys.** Each device and host has one long-term P-256 key pair. The private half stays on its machine; this desktop keeps its own in `e2e_key.json` in the state dir (mode 0600, private scalar only, base64url), via `internal/remote`. The public half is registered with the relay.
- **Handshake.** For each terminal socket both sides add a fresh ephemeral P-256 pair. Three ECDH terms (device ephemeral with host static, device static with host ephemeral, ephemeral with ephemeral) are concatenated and fed to HKDF-SHA256 salted with both ephemeral public keys. Output: two AES-256-GCM keys (one per direction) and two 4-byte nonce prefixes, each from its own info string beginning "flockdeck terminal e2e v1".
- **Frames.** Version byte (1), 8-byte big-endian counter, GCM ciphertext and tag. AAD is version and sender role. The receiver accepts only the exact next counter, so replay, reorder, drop and tampering fail `Open`.
- **Forward secrecy.** Past sessions cannot be recomputed without the discarded ephemeral keys.
- **Limit.** A relay that swaps a static public key at registration is not stopped by the protocol. `Fingerprint` (six 5-digit groups of SHA-256 over both static keys, device first) lets a person compare both screens.
- **Maintenance.** The package duplicates the relay's own implementation by hand; both must change together.
