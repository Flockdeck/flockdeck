---
type: Data Type
title: Jev question and answer types
description: The three question kinds Flockdeck can put to Jev and the answers they produce.
resource: internal/jev/question.go
tags: [jev, questions]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/jev/question.go]
---
# Jev questions

Wire question: `{type, instructions, criteria?}`. Request: `{state, model, questions: {key: question}}`. At least one question; keys are non-empty; questions are checked in key order so the same error is named each time.

| Go type | `type` | Criteria | Limits |
|---|---|---|---|
| `Noul` | `noul` | optional `{true, false}` meanings | instructions required |
| `Choice` | `choice` | map option name to description | 1 to 255 options, names non-empty |
| `Score` | `score` | ordered levels | 2 to 10 levels |

## Answers

- `NoulAnswer{Probability}` 0 to 1.
- `ChoiceAnswer{Choice, Probabilities, Confidence}`. Confidence measures how concentrated the probabilities are, not the chance that the choice is right.
- `ScoreAnswer{Score, Legend, Probabilities, Confidence}`: 0 to levels-1, fractional when spread over levels.

`decode` fails with `ErrBadResponse` if an answer is missing or of another type than its question. See [jev](/packages/jev.md) and [the request](/api/jev-systemone.md).
