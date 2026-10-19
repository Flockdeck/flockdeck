---
type: Package
title: spend
description: In-memory book of per-pane token and cost totals and per-account usage windows.
resource: internal/spend
tags: [cost, usage]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/spend/spend.go]
---
Holds what agents report about spend. Nothing is fetched: reports arrive over the loopback hook server. Used by the root program, `internal/chat`, `internal/hooks`, `internal/server`.

| Name | Role |
|---|---|
| `Report` | One pane's report: pane id (JSON `session`), conversation, model, account, `Tokens`, `Cost`, `Cumulative` flag, `Windows`. |
| `Tokens` | in, out, cacheRead, cacheWrite5m, cacheWrite1h, reasoning. |
| `Cost` | `USD`, `Known` (false means USD is a floor), `Source` ("table" or "agent"; "gateway" explains an unpriced call), `Checked`. |
| `Window` | A usage limit (`five_hour`, `seven_day` ...) keyed by account + name; `Used`, `Limit`, `Percent`, `ResetsAt`, `Seen`. |
| `Book` | `Add(report, now)`, `Retain(keep)`, `Pane(id, now)` returning `PaneView`/`WindowView`. |

## Behaviour

- A new `Conversation` id resets that pane's figures.
- `Cumulative` reports replace totals; others add.
- Windows belong to the account, not the pane. Expired windows are dropped. Within one reset time the larger `Used` wins; a later `ResetsAt` is a new window. The same figure from an idle pane is not treated as a fresh reading.
- `WarnAt` 80 and `FullAt` 95 percent give the levels "warn" and "full".
- Figures are estimates (prices from [pricing](/packages/pricing.md) or the agent's list prices); tokens are exact. The account is never derived from a key.
