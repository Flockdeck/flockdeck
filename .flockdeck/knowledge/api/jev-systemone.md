---
type: HTTP Endpoint
title: POST /v1/systemone (TypeSafe, outbound)
description: The one outbound call to TypeSafe's Jev API, carrying a scrubbed state and typed questions.
resource: internal/jev/jev.go
tags: [jev, typesafe, outbound]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jev/jev.go]
---
# POST /v1/systemone

An outbound request, not a route of Flockdeck. Base `https://api.typesafe.ai` (`jev.API`), path `/v1/systemone`, made by `Client.Ask` from [jev](/packages/jev.md) and only through [jevgate](/packages/jevgate.md).

## Request

Headers: `Authorization: Bearer <key>`, `Content-Type` and `Accept` JSON. Body: `{state, model, questions}` where `model` defaults to `jev-latest` and each question is a [Jev question](/data/jev-question.md). Questions are validated locally first so a 422 is avoided.

## Behaviour

- 429 and 529 are retried: 3 attempts, backoff starting at 500 ms doubling to 5 s, `Retry-After` honoured up to 10 s. 401 and 422 are never retried; a dropped connection is not retried.
- Each attempt has a 10 s timeout; the answer is read up to 4 MiB; a refusal body in errors is clipped to 300 bytes.
- Redirects are never followed (the key would travel with them).
- Answer: `Result{Model, Answers, Usage}`; each answer must match its question's type or `ErrBadResponse`.

Data handling: [Jev data handling](/security/jev-data-handling.md).
