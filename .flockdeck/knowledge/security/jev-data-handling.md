---
type: Security Boundary
title: Jev data handling
description: What may be sent to TypeSafe's Jev API, which key is used, and the guards in the client and gate.
resource: internal/jev
tags: [security, privacy, jev]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jev/jev.go, internal/jev/scrub.go, internal/record/redact.go, internal/secretname/secretname.go]
---
# Jev data handling

- **Single path.** Only [jevgate](/packages/jevgate.md) and the jev package call `Client.Ask`; a test enforces it, so no feature can send text that skipped scrubbing.
- **Scrubbing before sending.** `jev.Scrub`: unwrap hard-wrapped tokens, `record.Redact`, replace words that name secret files (`secretname`), replace runs of 3 or more base64-looking lines. Empty after scrubbing means no call. It is best effort and pattern-based. The caller then bounds the length.
- **Key.** Settings key first, then `TYPESAFE_API_KEY` ([env](/config/typesafe-api-key.md)). `FLOCKDECK_API_KEY` and vendor keys are never consulted. The key goes only in the `Authorization` header and never in logs or error messages.
- **Redirects** are refused (Go would carry the header across a redirect to http).
- **Rate and failure limits** in the gate: per-minute cap, per-scope interval and hourly limits, pause after a failure (longer after a refusal such as 401 or 422), reservation taken before the call.
- **No key, no call.** A missing key is `ErrNoKey` before any request.

How the feature is switched on is described in [Jev status assist privacy](/security/jev-status-assist-privacy.md).
