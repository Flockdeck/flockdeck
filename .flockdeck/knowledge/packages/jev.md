---
type: Package
title: jev
description: Client for TypeSafe's Jev structured-decision API, plus the scrubber for text sent to it.
resource: internal/jev
tags: [jev, typesafe, client, scrubbing]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jev/jev.go, internal/jev/question.go, internal/jev/scrub.go]
---
# internal/jev

The wire to Jev and nothing else. Callers hand it a state and a map of typed questions and get calibrated answers back.

## API

- `Client{APIBase, Key, Model, HTTP}`; `NewClient(stored KeyFunc)`, `NewClientFromEnv()`; `Key(stored)`.
- `Client.Ask(ctx, state, map[string]Question) (*Result, error)`. Details: [Jev system-one request](/api/jev-systemone.md).
- Questions: `Noul` (probability a statement holds), `Choice` (pick from named options, 1 to 255), `Score` (rating on an ordered rubric, 2 to 10 levels). Answers: `NoulAnswer`, `ChoiceAnswer`, `ScoreAnswer`. See [Jev question types](/data/jev-question.md).
- `Scrub(text)`: unwrap hard-wrapped tokens, `record.Redact`, replace words naming secret files (via `secretname`), replace runs of 3 or more base64-looking lines. Returns "" if only marks and white space remain.
- Errors: `ErrNoKey`, `ErrUnauthorized` (401), `ErrInvalidRequest` (422), `ErrRateLimited` (429), `ErrOverloaded` (529), `ErrBadResponse`, `*APIError`.

## Key

Read from Settings first (via `KeyFunc`), then `TYPESAFE_API_KEY` ([env](/config/typesafe-api-key.md)). `FLOCKDECK_API_KEY` is deliberately not used. See [Jev data handling](/security/jev-data-handling.md).

## Used by

[jevgate](/packages/jevgate.md) is the only caller of `Ask` (a test enforces it).
